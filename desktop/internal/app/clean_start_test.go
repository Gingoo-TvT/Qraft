package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshClientHasNoImplicitService(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{}
	m := NewManager(dir, runner, RuntimeManifest{})
	state, err := m.Snapshot()
	if err != nil || state.Configured || state.ServiceURL != "" || state.Config.ServerURL != "" || state.Config.LocalPort != 18180 {
		t.Fatalf("fresh state=%+v err=%v", state, err)
	}
	if _, err = m.ServerURL(); err == nil {
		t.Fatal("fresh client routed an implicit service")
	}
	if len(runner.calls) != 0 {
		t.Fatal("fresh client inspected Docker before user choice")
	}
	if _, err = os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("fresh state persisted a connection")
	}
	if err = SaveConfig(dir, DefaultConfig()); err == nil {
		t.Fatal("remote mode accepted empty saved address")
	}
}

func TestLocalSelectionCannotConnectToOccupiedForeignPort(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c := DefaultConfig()
	c.Mode, c.LocalPort, c.ServerURL = "local", listener.Addr().(*net.TCPAddr).Port, "http://localhost:18080"
	m := NewManager(t.TempDir(), &fakeRunner{}, fixtureManifest())
	if err := m.Save(c); err != nil {
		t.Fatal(err)
	}
	state, err := m.Snapshot()
	if err != nil || !state.Configured || state.ServiceURL != "" || state.Config.ServerURL != "" {
		t.Fatalf("%+v %v", state, err)
	}
	if _, err := m.ServerURL(); err == nil {
		t.Fatal("unstarted local mode connected to a foreign listener")
	}
	if err := m.checkLocalPort(context.Background(), c.LocalPort); err == nil {
		t.Fatal("accepted foreign occupied port")
	}
}

func TestOwnershipRejectsOrphanedVolumesAndNetworks(t *testing.T) {
	for _, kind := range []string{"volume", "network"} {
		for _, sameOwner := range []bool{false, true} {
			t.Run(kind+map[bool]string{true: "-owned", false: "-foreign"}[sameOwner], func(t *testing.T) {
				f := &fakeRunner{}
				m := NewManager(t.TempDir(), f, fixtureManifest())
				if err := m.prepare(18180); err != nil {
					t.Fatal(err)
				}
				env, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
				f.fn = func(args ...string) (string, error) {
					if args[0] == kind && args[1] == "ls" {
						return Project + "_retained", nil
					}
					if args[0] == kind && args[1] == "inspect" {
						labels := map[string]string{}
						if sameOwner {
							labels["io.qraft.desktop.owner"] = env["QRAFT_DESKTOP_OWNER"]
						}
						b, _ := json.Marshal(labels)
						return string(b), nil
					}
					return "", nil
				}
				err := m.ownership(context.Background())
				if (err == nil) != sameOwner {
					t.Fatalf("owner=%v error=%v", sameOwner, err)
				}
				for _, call := range f.calls {
					if strings.Contains(call, " down") || strings.Contains(call, " rm") {
						t.Fatal("ownership check changed resources")
					}
				}
			})
		}
	}
}

func TestLocalRoutingRequiresOwnedGatewayAndMatchingPort(t *testing.T) {
	for _, tc := range []struct {
		name, owner, port string
		ready             bool
	}{
		{"owned", "same", "18180", true}, {"foreign", "foreign", "18180", false}, {"wrong-port", "same", "18080", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{}
			m := NewManager(t.TempDir(), f, fixtureManifest())
			if err := m.prepare(18180); err != nil {
				t.Fatal(err)
			}
			env, _ := readEnv(filepath.Join(m.runtimeDir(), ".env"))
			c := DefaultConfig()
			c.Mode = "local"
			if err := m.Save(c); err != nil {
				t.Fatal(err)
			}
			owner := tc.owner
			if owner == "same" {
				owner = env["QRAFT_DESKTOP_OWNER"]
			}
			f.fn = func(args ...string) (string, error) {
				if args[0] == "ps" {
					return "gateway-id", nil
				}
				b, _ := json.Marshal(map[string]any{
					"labels": map[string]string{"io.qraft.desktop.owner": owner},
					"ports":  map[string]any{"80/tcp": []map[string]string{{"HostIP": "127.0.0.1", "HostPort": tc.port}}},
				})
				return string(b), nil
			}
			url, err := m.ServerURL()
			if (err == nil) != tc.ready || (tc.ready && url != "http://127.0.0.1:18180") {
				t.Fatalf("url=%s err=%v", url, err)
			}
		})
	}
}
