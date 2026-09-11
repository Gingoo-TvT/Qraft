package main

import (
	"strings"
	"testing"
)

func TestSandboxMountAndUmountSyscallsBlocked(t *testing.T) {
	baseURL := contractURL(t)
	source := `#define _GNU_SOURCE
#include <errno.h>
#include <stdio.h>
#include <sys/mount.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(void) {
  errno = 0;
  int mount_rc = mount("none", "/tmp/algoforge-mount-probe", "tmpfs", 0, "size=4096");
  int mount_errno = errno;
  errno = 0;
  int umount_rc = umount("/tmp");
  int umount_errno = errno;
  errno = 0;
  long raw_rc = syscall(SYS_umount2, "/tmp", MNT_DETACH);
  int raw_errno = errno;
  printf("mount=%d/%d umount=%d/%d raw_umount2=%ld/%d\n",
    mount_rc, mount_errno, umount_rc, umount_errno, raw_rc, raw_errno);
  if (mount_rc == -1 && mount_errno == EPERM &&
      umount_rc == -1 && umount_errno == EPERM &&
      raw_rc == -1 && raw_errno == EPERM) {
    puts("BLOCKED");
    return 0;
  }
  return 2;
}`
	response := contractExecute(t, baseURL, "c", source, []string{""}, executionLimits{
		TimeLimitMS: 2000, MemoryLimitMB: 128, OutputLimitBytes: 64 << 10, MaxProcesses: 32,
	})
	if !response.Compile.Success || len(response.Results) != 1 {
		t.Fatalf("mount probe compile/results failed: %+v", response)
	}
	wantPolicyDigest := sha256String(seccompPolicy)
	if response.Audit.SeccompPolicyDigest != wantPolicyDigest || response.Compile.Audit.SeccompPolicyDigest != wantPolicyDigest {
		t.Fatalf("mount probe policy digest mismatch: execute=%q compile=%q want=%q",
			response.Audit.SeccompPolicyDigest, response.Compile.Audit.SeccompPolicyDigest, wantPolicyDigest)
	}
	result := response.Results[0]
	if result.Verdict != verdictOK || !strings.Contains(result.Stdout, "BLOCKED") || !strings.Contains(result.Stdout, "raw_umount2=-1/1") {
		t.Fatalf("mount probe escaped or was not observable: %+v", result)
	}
	t.Log("mount, libc umount, and raw SYS_umount2 all returned EPERM inside nsjail")
}
