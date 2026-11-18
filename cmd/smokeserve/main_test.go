package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTheHarnessServesTheRealServiceAndTheFakeTMDBBehindIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, in := io.Pipe()
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"--listen", "127.0.0.1:0", "--dir", t.TempDir()}, in, io.Discard) }()

	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("no ready line: %v", err)
	}
	base := "http://" + strings.TrimSpace(strings.TrimPrefix(line, "listening on "))
	resp, err := http.Get(base + "/api/search?q=dune")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"id":"movie-438631"`) {
		t.Errorf("search = %d %s", resp.StatusCode, body)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the harness did not stop")
	}
}
