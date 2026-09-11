package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if os.Getpid() == 1 {
		runInitSupervisor()
		return
	}
	runServer()
}

func runInitSupervisor() {
	if err := prepareCgroupV2("/sys/fs/cgroup", os.Getpid()); err != nil {
		log.Fatalf("preparing cgroup v2 delegation: %v", err)
	}

	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("starting sandbox service child: %v", err)
	}

	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			log.Fatalf("reaping sandbox process: %v", err)
		}
		if pid != cmd.Process.Pid {
			continue
		}
		if status.Exited() {
			os.Exit(status.ExitStatus())
		}
		if status.Signaled() {
			os.Exit(128 + int(status.Signal()))
		}
		os.Exit(1)
	}
}

func runServer() {
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		log.Fatalf("unsupported command %q", os.Args[1])
	}

	port := strings.TrimSpace(os.Getenv("SANDBOX_PORT"))
	if port == "" {
		port = "8090"
	}

	maxConcurrent := 5
	if value := strings.TrimSpace(os.Getenv("SANDBOX_MAX_CONCURRENT")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 128 {
			log.Fatalf("invalid SANDBOX_MAX_CONCURRENT %q", value)
		}
		maxConcurrent = parsed
	}
	executeLimits, err := loadDeploymentExecuteLimits(os.LookupEnv)
	if err != nil {
		log.Fatal(err)
	}

	revision := strings.TrimSpace(os.Getenv("SANDBOX_REVISION"))
	imageDigest := strings.TrimSpace(os.Getenv("SANDBOX_IMAGE_DIGEST"))
	if err := validateDeploymentIdentity(revision, imageDigest); err != nil {
		log.Fatal(err)
	}
	bakedRevision, err := readBakedRevision(bakedRevisionPath)
	if err != nil {
		log.Fatal(err)
	}
	if err := validateBakedRevision(revision, bakedRevision); err != nil {
		log.Fatal(err)
	}
	manifestPath := strings.TrimSpace(os.Getenv("SANDBOX_TOOLCHAIN_MANIFEST"))
	if manifestPath == "" {
		manifestPath = "/sandbox/app/toolchain-packages.txt"
	}
	toolchainManifestDigest, err := sha256File(manifestPath)
	if err != nil {
		log.Fatalf("hashing toolchain manifest: %v", err)
	}
	auditPath := strings.TrimSpace(os.Getenv("SANDBOX_AUDIT_LOG"))
	if auditPath == "" {
		auditPath = "/sandbox/audit/audit.jsonl"
	}
	audit, err := openAuditSink(auditPath)
	if err != nil {
		log.Fatalf("opening persistent audit log: %v", err)
	}
	defer audit.close()

	engine := newProductionEngine(os.Getenv("SANDBOX_NSJAIL_BINARY"), revision)
	engine.imageDigest = imageDigest
	engine.toolchainManifestDigest = toolchainManifestDigest
	engine.cgroupErr = prepareCgroupV2("/sys/fs/cgroup", os.Getpid())
	if engine.cgroupErr != nil {
		log.Printf("sandbox will remain fail-closed: %v", engine.cgroupErr)
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           newServerWithExecuteLimits(engine, maxConcurrent, audit, executeLimits),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       45 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("sandbox health server listening on :%s", port)
		errCh <- server.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatal(err)
	}
}

func loadDeploymentExecuteLimits(lookupEnv func(string) (string, bool)) (executeLimitCeiling, error) {
	timeLimitSeconds, err := parseRequiredDecimalEnv(
		lookupEnv, "SANDBOX_TIME_LIMIT", 1, protocolMaxTimeLimitMS/1000,
	)
	if err != nil {
		return executeLimitCeiling{}, err
	}
	memoryLimitMB, err := parseRequiredDecimalEnv(
		lookupEnv, "SANDBOX_MEMORY_LIMIT", protocolMinMemoryLimitMB, protocolMaxMemoryLimitMB,
	)
	if err != nil {
		return executeLimitCeiling{}, err
	}
	return executeLimitCeiling{
		TimeLimitMS:   timeLimitSeconds * 1000,
		MemoryLimitMB: memoryLimitMB,
	}, nil
}

func parseRequiredDecimalEnv(lookupEnv func(string) (string, bool), name string, minValue, maxValue int) (int, error) {
	raw, ok := lookupEnv(name)
	if !ok || raw == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	for _, char := range raw {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("invalid %s %q: must be a decimal integer between %d and %d", name, raw, minValue, maxValue)
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		return 0, fmt.Errorf("invalid %s %q: must be a decimal integer between %d and %d", name, raw, minValue, maxValue)
	}
	return value, nil
}
