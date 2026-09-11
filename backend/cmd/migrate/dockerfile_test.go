package main

import (
	"os"
	"strings"
	"testing"
)

func TestDockerfilePackagesMigrationArtifactWithoutChangingDefaultTarget(t *testing.T) {
	data, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatalf("read backend Dockerfile: %v", err)
	}
	dockerfile := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, required := range []string{"ARG TARGETOS=linux", "ARG TARGETARCH=amd64", "GOOS=${TARGETOS} GOARCH=${TARGETARCH}"} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile is missing target-platform build contract %q", required)
		}
	}
	if strings.Contains(dockerfile, "GOARCH=amd64 go build") {
		t.Fatal("Dockerfile hardcodes amd64 binaries into target-platform runtime images")
	}

	buildCommand := "-o /app/bin/migrate     ./cmd/migrate"
	if !strings.Contains(dockerfile, buildCommand) {
		t.Fatalf("Dockerfile does not build cmd/migrate into /app/bin/migrate")
	}
	if !strings.Contains(dockerfile, "migration-chain.sha256") || !strings.Contains(dockerfile, "migrations.sha256") {
		t.Fatal("Dockerfile does not bind the migration artifact to an ordered SQL-chain manifest")
	}

	migrationsStart := strings.Index(dockerfile, " AS migrations\n")
	productionStart := strings.Index(dockerfile, " AS production\n")
	if migrationsStart < 0 || productionStart < 0 {
		t.Fatalf("Dockerfile is missing migrations or production target")
	}
	if migrationsStart >= productionStart {
		t.Fatalf("migrations target must precede production so production remains the default final target")
	}

	target := dockerfile[migrationsStart:productionStart]
	for _, required := range []string{
		"ARG SOURCE_REVISION",
		"ENV MIGRATIONS_DIR=/app/migrations",
		"COPY --from=builder /app/bin/migrate /app/migrate",
		"COPY --from=builder /app/migrations /app/migrations",
		"COPY --from=builder /app/bin/migrations.sha256 /app/migrations.sha256",
		"COPY --from=builder /app/bin/migration-chain.sha256 /app/migration-chain.sha256",
		"/app/source-revision.txt",
		"LABEL org.opencontainers.image.revision=\"${SOURCE_REVISION}\"",
		"USER algoforge-migrate",
		"ENTRYPOINT [\"/app/migrate\"]",
	} {
		if !strings.Contains(target, required) {
			t.Errorf("migrations target is missing %q", required)
		}
	}
}
