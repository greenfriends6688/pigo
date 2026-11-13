package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTmpPiRPC(t *testing.T) {
	pi := DiscoverPi()
	if pi == "" {
		t.Skip("no pi")
	}
	cwd := "/Users/yingjing/Desktop/重构pi web"
	tr, err := NewStdioTransport(Options{
		PiPath:   pi,
		Cwd:      cwd,
		AgentDir: filepath.Join(os.Getenv("HOME"), ".pi", "agent"),
		LogPath:  filepath.Join(t.TempDir(), "agent.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	frames := make(chan Frame, 8)
	cl := NewClient(tr, func(f Frame) {}, func(error) {})
	_ = frames
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	resp, err := cl.Call(ctx, Command{"type": CmdGetState})
	t.Logf("get_state took %s err=%v", time.Since(start).Round(time.Millisecond), err)
	if resp != nil {
		t.Logf("ok")
	}
	_ = cl.Close()
}
