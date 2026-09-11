package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSandboxVerdictContract150 exercises the real HTTP service and nsjail.
// AC/WA below are harness-level judge outcomes: the sandbox itself returns OK
// for both and never receives expected output. CE is compile.success=false.
// It is intentionally opt-in because it requires a privileged Linux sandbox
// container with cgroup v2 delegation and all five pinned toolchains.
func TestSandboxVerdictContract150(t *testing.T) {
	baseURL := contractURL(t)
	inputs := []string{"2\n", "3\n", "4\n", "5\n", "6\n"}
	total := 0
	for _, language := range []string{"c", "cpp", "python3", "java", "go"} {
		language := language
		t.Run(language, func(t *testing.T) {
			sources := verdictSources[language]
			for _, category := range []string{"AC", "WA", "CE", "RE", "TLE", "MLE"} {
				category := category
				t.Run(category, func(t *testing.T) {
					limits := executionLimits{TimeLimitMS: 2000, MemoryLimitMB: 128, OutputLimitBytes: 64 << 10, MaxProcesses: 64}
					if category == "TLE" {
						limits.TimeLimitMS = 150
					}
					if category == "MLE" {
						limits.TimeLimitMS = 3000
						limits.MemoryLimitMB = 64
					}
					resp := contractExecute(t, baseURL, language, sources[category], inputs, limits)
					if category == "CE" {
						if resp.Compile.Success || len(resp.Results) != 0 {
							t.Fatalf("CE contract violated: %+v", resp)
						}
						total += len(inputs)
						return
					}
					if !resp.Compile.Success || len(resp.Results) != len(inputs) {
						t.Fatalf("compile/results contract violated: %+v", resp)
					}
					for i, result := range resp.Results {
						expectedVerdict := category
						if category == "AC" || category == "WA" {
							expectedVerdict = verdictOK
						}
						if result.Verdict != expectedVerdict {
							t.Fatalf("case %d verdict=%s want=%s exit=%d signal=%s time_ms=%d stderr=%s", i, result.Verdict, expectedVerdict, result.ExitCode, result.Signal, result.TimeMS, result.Stderr)
						}
						if category == "AC" || category == "WA" {
							want := fmt.Sprintf("%d", (i+2)*2)
							matches := strings.TrimSpace(result.Stdout) == want
							if (category == "AC" && !matches) || (category == "WA" && matches) {
								t.Fatalf("case %d output=%q gold=%q category=%s", i, result.Stdout, want, category)
							}
						}
						total++
					}
				})
			}
		})
	}
	if total != 150 {
		t.Fatalf("verdict matrix count=%d want=150", total)
	}
	t.Logf("sandbox protocol + harness judge matrix passed: %d/150 (AC/WA are harness labels, CE is compile failure)", total)
}

func TestSandboxRepeatability100(t *testing.T) {
	baseURL := contractURL(t)
	limits := executionLimits{TimeLimitMS: 2000, MemoryLimitMB: 128, OutputLimitBytes: 64 << 10, MaxProcesses: 32}
	const repeats = 100
	var manifest string
	runIDs := make(map[string]struct{}, repeats)
	for i := 0; i < repeats; i++ {
		response := contractExecuteSeed(t, baseURL, "c", verdictSources["c"]["AC"], []string{"7\n"}, limits, 42)
		if manifest == "" {
			manifest = response.Audit.ManifestDigest
		} else if response.Audit.ManifestDigest != manifest {
			t.Fatalf("repeat %d manifest drift: %s != %s", i, response.Audit.ManifestDigest, manifest)
		}
		if response.Audit.Seed != 42 {
			t.Fatalf("repeat %d seed=%d want=42", i, response.Audit.Seed)
		}
		if _, duplicate := runIDs[response.Audit.RunID]; duplicate {
			t.Fatalf("repeat %d reused run_id %s", i, response.Audit.RunID)
		}
		runIDs[response.Audit.RunID] = struct{}{}
		if len(response.Results) != 1 {
			t.Fatalf("repeat %d result count=%d want=1", i, len(response.Results))
		}
		result := response.Results[0]
		if result.Verdict != verdictOK || strings.TrimSpace(result.Stdout) != "14" || result.ExitCode != 0 {
			t.Fatalf("repeat %d mismatch: %+v", i, result)
		}
	}
	t.Logf("repeatability passed: %d independent requests; manifest/seed/output/verdict stable and run IDs unique", repeats)
}

func TestSandboxStackLimitTracksMemoryContract(t *testing.T) {
	baseURL := contractURL(t)
	source := `#include <cstdint>
#include <iostream>
__attribute__((noinline)) std::uint64_t consume_stack(int depth) {
  volatile unsigned char frame[1024] = {};
  frame[depth & 1023] = static_cast<unsigned char>(depth);
  if (depth == 0) return frame[0];
  const std::uint64_t child = consume_stack(depth - 1);
  return child + frame[(depth * 17) & 1023];
}
int main() {
  std::cout << consume_stack(12000) << '\n';
}`
	limits := executionLimits{
		TimeLimitMS: 5000, MemoryLimitMB: 64, OutputLimitBytes: 64 << 10, MaxProcesses: 32,
	}
	resp := contractExecute(t, baseURL, "cpp", source, []string{""}, limits)
	if !resp.Compile.Success || len(resp.Results) != 1 {
		t.Fatalf("deep-stack compile/results failed: %+v", resp)
	}
	result := resp.Results[0]
	if result.Verdict != verdictOK || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) == "" {
		t.Fatalf("deep-stack execution failed: %+v", result)
	}
	if !strings.Contains(resp.Audit.LimitProfile, "memory_mb=64,stack_mb=64") {
		t.Fatalf("stack limit is not bound to the execution receipt: %q", resp.Audit.LimitProfile)
	}
}

func TestSandboxRuntimeIdentityContract(t *testing.T) {
	baseURL := contractURL(t)
	source := `#define _GNU_SOURCE
#include <errno.h>
#include <sched.h>
#include <stdio.h>
#include <unistd.h>
int main(void) {
  char uidmap[256] = {0}, cap[256] = {0}, line[256];
  FILE *f = fopen("/proc/self/uid_map", "r");
  if (f) { fgets(uidmap, sizeof(uidmap), f); fclose(f); }
  f = fopen("/proc/self/status", "r");
  if (f) { while (fgets(line, sizeof(line), f)) if (sscanf(line, "CapEff:%255s", cap) == 1) break; fclose(f); }
  errno = 0; int unshare_rc = unshare(0); int unshare_errno = errno;
  printf("uid=%d gid=%d\nuidmap=%s\ncap=%s\nunshare=%d errno=%d\n", getuid(), getgid(), uidmap, cap, unshare_rc, unshare_errno);
}`
	resp := contractExecuteSeed(t, baseURL, "c", source, []string{""}, executionLimits{
		TimeLimitMS: 2000, MemoryLimitMB: 128, OutputLimitBytes: 64 << 10, MaxProcesses: 32,
	}, 7)
	if !resp.Compile.Success || len(resp.Results) != 1 || resp.Results[0].Verdict != verdictOK {
		t.Fatalf("identity probe failed: %+v", resp)
	}
	out := resp.Results[0].Stdout
	for _, required := range []string{"uid=65534 gid=65534", "uidmap=     65534      65534          1", "cap=0000000000000000", "unshare=-1 errno=1"} {
		if !strings.Contains(out, required) {
			t.Fatalf("identity probe missing %q: %s", required, out)
		}
	}
	t.Logf("runtime identity passed: global nobody mapping, zero capabilities, seccomp unshare block")
}

func TestSandboxFailClosedContract(t *testing.T) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("SANDBOX_FAILCLOSED_URL")), "/")
	if baseURL == "" {
		t.Skip("set SANDBOX_FAILCLOSED_URL to a service started with a missing nsjail binary")
	}
	body, _ := json.Marshal(compileRequest{Version: apiVersion, Language: "c", Source: "int main(void){return 0;}"})
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Post(baseURL+"/v1/compile", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(data), `"code":"sandbox_unavailable"`) {
		t.Fatalf("fail-closed response HTTP %d: %s", resp.StatusCode, data)
	}
	t.Log("missing nsjail failed closed with HTTP 503; no host execution path was used")
}

func TestSandboxEscapeContract25(t *testing.T) {
	baseURL := contractURL(t)
	inputs := []string{"process\n", "network\n", "traversal\n", "host-read-write\n", "output-flood\n"}
	limits := executionLimits{
		TimeLimitMS: 3000, MemoryLimitMB: 256, OutputLimitBytes: 64 << 10, MaxProcesses: 32,
	}
	total := 0
	for _, language := range []string{"c", "cpp", "python3", "java", "go"} {
		resp := contractExecute(t, baseURL, language, escapeSources[language], inputs, limits)
		if !resp.Compile.Success || len(resp.Results) != len(inputs) {
			t.Fatalf("%s escape compile/results failed: %+v", language, resp)
		}
		for i, result := range resp.Results {
			fixture := strings.TrimSpace(inputs[i])
			if fixture == "output-flood" {
				if result.Verdict != verdictOLE || int64(len(result.Stdout)+len(result.Stderr)) > limits.OutputLimitBytes {
					t.Fatalf("%s output flood was not contained: verdict=%s bytes=%d stderr=%q", language, result.Verdict, len(result.Stdout)+len(result.Stderr), result.Stderr)
				}
				total++
				continue
			}
			processBlockedBySandbox := fixture == "process" && result.Verdict != verdictOK
			if !processBlockedBySandbox && (result.Verdict != verdictOK || strings.TrimSpace(result.Stdout) != "BLOCKED") {
				t.Fatalf("%s escape %q was not blocked: verdict=%s stdout=%q stderr=%q", language, strings.TrimSpace(inputs[i]), result.Verdict, result.Stdout, result.Stderr)
			}
			total++
		}
	}
	if total != 25 {
		t.Fatalf("escape matrix count=%d want=25", total)
	}
	t.Logf("escape matrix passed: %d/25", total)
}

func contractURL(t *testing.T) string {
	t.Helper()
	value := strings.TrimRight(strings.TrimSpace(os.Getenv("SANDBOX_CONTRACT_URL")), "/")
	if value == "" {
		t.Skip("set SANDBOX_CONTRACT_URL to run privileged nsjail contracts")
	}
	return value
}

func contractExecute(t *testing.T, baseURL, language, source string, inputs []string, limits executionLimits) executeResponse {
	return contractExecuteSeed(t, baseURL, language, source, inputs, limits, 0)
}

func contractExecuteSeed(t *testing.T, baseURL, language, source string, inputs []string, limits executionLimits, seed int64) executeResponse {
	t.Helper()
	body, err := json.Marshal(executeRequest{Version: apiVersion, Language: language, Source: source, Inputs: inputs, Limits: limits, Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/execute", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Minute, Transport: &http.Transport{Proxy: nil}}
	httpResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("execute %s: %v", language, err)
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(httpResp.Body, 4<<20))
	if err != nil {
		t.Fatal(err)
	}
	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("execute %s HTTP %d: %s", language, httpResp.StatusCode, data)
	}
	var response executeResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode %s response: %v body=%s", language, err, data)
	}
	if response.Audit.RunID == "" || response.Audit.ManifestDigest == "" || response.Audit.ImageDigest == "" || response.Audit.LimitProfile == "" {
		t.Fatalf("execute %s missing audit metadata: %+v", language, response.Audit)
	}
	if response.Compile.Audit.RunID != response.Audit.RunID {
		t.Fatalf("execute %s compile run_id=%q, execute run_id=%q", language, response.Compile.Audit.RunID, response.Audit.RunID)
	}
	return response
}

var verdictSources = map[string]map[string]string{
	"c": {
		"AC":  "#include <stdio.h>\nint main(void){long x;if(scanf(\"%ld\",&x)!=1)return 1;printf(\"%ld\\n\",x*2);}",
		"WA":  "#include <stdio.h>\nint main(void){long x;if(scanf(\"%ld\",&x)!=1)return 1;printf(\"%ld\\n\",x+7);}",
		"CE":  "int main(void){ syntax error }",
		"RE":  "int main(void){volatile int *p=(int*)0;*p=1;}",
		"TLE": "#include <unistd.h>\nint main(void){sleep(5);}",
		"MLE": "#include <stdlib.h>\n#include <string.h>\n#include <stdio.h>\nint main(void){for(;;){void*p=malloc(8388608);if(!p){fputs(\"cannot allocate memory\",stderr);return 1;}memset(p,1,8388608);}}",
	},
	"cpp": {
		"AC":  "#include <iostream>\nint main(){long x;if(!(std::cin>>x))return 1;std::cout<<x*2<<'\\n';}",
		"WA":  "#include <iostream>\nint main(){long x;if(!(std::cin>>x))return 1;std::cout<<x+7<<'\\n';}",
		"CE":  "int main(){ syntax error }",
		"RE":  "int main(){volatile int*p=nullptr;*p=1;}",
		"TLE": "#include <unistd.h>\nint main(){sleep(5);}",
		"MLE": "#include <vector>\n#include <iostream>\nint main(){try{std::vector<std::vector<char>>x;for(;;)x.emplace_back(8388608,1);}catch(...){std::cerr<<\"cannot allocate memory\";return 1;}}",
	},
	"python3": {
		"AC":  "x=int(input())\nprint(x*2)\n",
		"WA":  "x=int(input())\nprint(x+7)\n",
		"CE":  "def broken(:\n",
		"RE":  "raise RuntimeError('fixture')\n",
		"TLE": "while True:\n    pass\n",
		"MLE": "import sys\ntry:\n    x=bytearray(512*1024*1024)\nexcept MemoryError:\n    sys.stderr.write('cannot allocate memory')\n    sys.exit(1)\n",
	},
	"java": {
		"AC":  "import java.util.*; public class Main{public static void main(String[]a){Scanner s=new Scanner(System.in);long x=s.nextLong();System.out.println(x*2);}}",
		"WA":  "import java.util.*; public class Main{public static void main(String[]a){Scanner s=new Scanner(System.in);long x=s.nextLong();System.out.println(x+7);}}",
		"CE":  "public class Main { syntax error }",
		"RE":  "public class Main{public static void main(String[]a){throw new RuntimeException(\"fixture\");}}",
		"TLE": "public class Main{public static void main(String[]a){for(;;){}}}",
		"MLE": "import java.util.*; public class Main{public static void main(String[]a){ArrayList<byte[]>x=new ArrayList<>();for(;;)x.add(new byte[8388608]);}}",
	},
	"go": {
		"AC":  "package main\nimport \"fmt\"\nfunc main(){var x int;fmt.Scan(&x);fmt.Println(x*2)}\n",
		"WA":  "package main\nimport \"fmt\"\nfunc main(){var x int;fmt.Scan(&x);fmt.Println(x+7)}\n",
		"CE":  "package main\nfunc main( {\n",
		"RE":  "package main\nfunc main(){panic(\"fixture\")}\n",
		"TLE": "package main\nfunc main(){for{}}\n",
		"MLE": "package main\nfunc main(){x:=make([]byte,512<<20);var s byte;for i:=range x{x[i]=byte(i);s+=x[i]};println(s,x[123])}\n",
	},
}

var escapeSources = map[string]string{
	"c": `#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <unistd.h>
int main(void){char m[32]={0};scanf("%31s",m);int blocked=0;
if(!strcmp(m,"process")){for(int i=0;i<128;i++){pid_t p=fork();if(p<0){blocked=1;break;}if(p==0){sleep(2);_exit(0);}}}
else if(!strcmp(m,"network")){int s=socket(AF_INET,SOCK_STREAM,0);struct sockaddr_in a={.sin_family=AF_INET,.sin_port=htons(80)};inet_pton(AF_INET,"1.1.1.1",&a.sin_addr);blocked=(s<0||connect(s,(void*)&a,sizeof(a))<0);}
else if(!strcmp(m,"traversal"))blocked=(access("../../../../sandbox/audit/audit.jsonl",R_OK)!=0&&access("/proc/1/root/sandbox/audit/audit.jsonl",R_OK)!=0);
else if(!strcmp(m,"host-read-write")){int r=open("/sandbox-host/secret",O_RDONLY);int w=open("/sandbox-host/escape-marker",O_WRONLY|O_CREAT,0600);blocked=(r<0&&w<0);}
else if(!strcmp(m,"output-flood")){for(;;)fputs("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",stdout);}
puts(blocked?"BLOCKED":"ESCAPED");return 0;}`,
	"cpp": `#include <arpa/inet.h>
#include <fcntl.h>
#include <iostream>
#include <string>
#include <sys/socket.h>
#include <unistd.h>
int main(){std::string m;std::cin>>m;bool b=false;
if(m=="process"){for(int i=0;i<128;i++){pid_t p=fork();if(p<0){b=true;break;}if(p==0){sleep(2);_exit(0);}}}
else if(m=="network"){int s=socket(AF_INET,SOCK_STREAM,0);sockaddr_in a{};a.sin_family=AF_INET;a.sin_port=htons(80);inet_pton(AF_INET,"1.1.1.1",&a.sin_addr);b=(s<0||connect(s,(sockaddr*)&a,sizeof(a))<0);}
else if(m=="traversal")b=(access("../../../../sandbox/audit/audit.jsonl",R_OK)!=0&&access("/proc/1/root/sandbox/audit/audit.jsonl",R_OK)!=0);
else if(m=="host-read-write"){int r=open("/sandbox-host/secret",O_RDONLY);int w=open("/sandbox-host/escape-marker",O_WRONLY|O_CREAT,0600);b=(r<0&&w<0);}
else if(m=="output-flood"){for(;;)std::cout<<"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx";}
std::cout<<(b?"BLOCKED":"ESCAPED")<<'\n';}`,
	"python3": `import os,socket,sys,time
m=input().strip(); b=False
if m=='process':
  for _ in range(128):
    try:
      p=os.fork()
      if p==0: time.sleep(2); os._exit(0)
    except OSError: b=True; break
elif m=='network':
  try: socket.create_connection(('1.1.1.1',80),.2)
  except OSError: b=True
elif m=='traversal': b=not os.access('../../../../sandbox/audit/audit.jsonl',os.R_OK) and not os.access('/proc/1/root/sandbox/audit/audit.jsonl',os.R_OK)
elif m=='host-read-write':
  try:
    open('/sandbox-host/secret').close(); read_blocked=False
  except OSError: read_blocked=True
  try:
    open('/sandbox-host/escape-marker','w').close(); write_blocked=False
  except OSError: write_blocked=True
  b=read_blocked and write_blocked
elif m=='output-flood':
  while True: sys.stdout.write('x'*4096); sys.stdout.flush()
print('BLOCKED' if b else 'ESCAPED')
`,
	"java": `import java.io.*;import java.net.*;import java.nio.file.*;import java.util.*;
public class Main{public static void main(String[]a)throws Exception{String m=new Scanner(System.in).next();boolean b=false;
if(m.equals("process")){for(int i=0;i<128;i++){try{new ProcessBuilder("/usr/bin/sleep","2").start();}catch(IOException e){b=true;break;}}}
else if(m.equals("network")){try{Socket s=new Socket();s.connect(new InetSocketAddress("1.1.1.1",80),200);}catch(IOException e){b=true;}}
else if(m.equals("traversal"))b=!Files.isReadable(Path.of("../../../../sandbox/audit/audit.jsonl"))&&!Files.isReadable(Path.of("/proc/1/root/sandbox/audit/audit.jsonl"));
else if(m.equals("host-read-write")){boolean r=false,w=false;try{Files.readString(Path.of("/sandbox-host/secret"));}catch(IOException e){r=true;}try{Files.writeString(Path.of("/sandbox-host/escape-marker"),"x");}catch(IOException e){w=true;}b=r&&w;}
else if(m.equals("output-flood")){for(;;)System.out.print("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx");}
System.out.println(b?"BLOCKED":"ESCAPED");}}`,
	"go": `package main
import("fmt";"net";"os";"time")
func main(){var m string;fmt.Scan(&m);b:=false
if m=="process"{ps:=[]*os.Process{};for i:=0;i<128;i++{p,e:=os.StartProcess("/usr/bin/sleep",[]string{"sleep","2"},&os.ProcAttr{Files:[]*os.File{nil,nil,nil}});if e!=nil{b=true;break};ps=append(ps,p)}}
if m=="network"{c,e:=net.DialTimeout("tcp","1.1.1.1:80",200*time.Millisecond);if e!=nil{b=true}else{c.Close()}}
if m=="traversal"{_,e1:=os.ReadFile("../../../../sandbox/audit/audit.jsonl");_,e2:=os.ReadFile("/proc/1/root/sandbox/audit/audit.jsonl");b=e1!=nil&&e2!=nil}
if m=="host-read-write"{_,e1:=os.ReadFile("/sandbox-host/secret");e2:=os.WriteFile("/sandbox-host/escape-marker",[]byte("x"),0600);b=e1!=nil&&e2!=nil}
if m=="output-flood"{for{fmt.Print("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")}}
if b{fmt.Println("BLOCKED")}else{fmt.Println("ESCAPED")}}
`,
}
