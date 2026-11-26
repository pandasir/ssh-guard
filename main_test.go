package main

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"ssh-guard/internal/sshproxy"
)

func TestIPCTransportCoalescingOrderingAndClose(t *testing.T) {
	var (
		mu     sync.Mutex
		events []map[string]any
	)

	tr := newIPCTransport(nil, "sess-1")
	tr.emitFn = func(payload map[string]any) {
		mu.Lock()
		events = append(events, payload)
		mu.Unlock()
	}

	// 1. Small output chunks within the flush interval should coalesce into a single event.
	for i := 0; i < 10; i++ {
		if err := tr.Send(sshproxy.WSMessage{Type: "output", Data: "a"}); err != nil {
			t.Fatalf("Send output failed: %v", err)
		}
	}

	// Sending a non-output control event must flush pending output first to preserve order.
	if err := tr.Send(sshproxy.WSMessage{Type: "guard_blocked", Data: "BLOCKED"}); err != nil {
		t.Fatalf("Send control failed: %v", err)
	}

	mu.Lock()
	if len(events) != 2 {
		t.Fatalf("expected 2 events (1 coalesced output + 1 control), got %d", len(events))
	}
	msg0 := events[0]["msg"].(sshproxy.WSMessage)
	msg1 := events[1]["msg"].(sshproxy.WSMessage)
	if msg0.Type != "output" || msg0.Data != "aaaaaaaaaa" {
		t.Fatalf("unexpected first event: %+v", msg0)
	}
	if msg1.Type != "guard_blocked" || msg1.Data != "BLOCKED" {
		t.Fatalf("unexpected second event: %+v", msg1)
	}
	events = nil
	mu.Unlock()

	// 2. Output exceeding ipcFlushMaxBytes should flush synchronously without waiting for timer.
	big := strings.Repeat("x", ipcFlushMaxBytes)
	if err := tr.Send(sshproxy.WSMessage{Type: "output", Data: big}); err != nil {
		t.Fatalf("Send big output failed: %v", err)
	}
	mu.Lock()
	if len(events) != 1 || events[0]["msg"].(sshproxy.WSMessage).Data != big {
		t.Fatalf("expected immediate flush for big output, got %d events", len(events))
	}
	events = nil
	mu.Unlock()

	// 3. Timed flush should deliver small output after ipcFlushInterval.
	if err := tr.Send(sshproxy.WSMessage{Type: "output", Data: "hello"}); err != nil {
		t.Fatalf("Send timed output failed: %v", err)
	}
	time.Sleep(ipcFlushInterval * 3)
	mu.Lock()
	if len(events) != 1 || events[0]["msg"].(sshproxy.WSMessage).Data != "hello" {
		t.Fatalf("expected timed flush to deliver 'hello', got %+v", events)
	}
	events = nil
	mu.Unlock()

	// 4. Close must flush any buffered output before emitting closed, and reject further sends.
	if err := tr.Send(sshproxy.WSMessage{Type: "output", Data: "tail"}); err != nil {
		t.Fatalf("Send tail failed: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := tr.Send(sshproxy.WSMessage{Type: "output", Data: "after"}); err != io.EOF {
		t.Fatalf("expected io.EOF after Close, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("expected 2 events on close (tail + closed), got %d: %+v", len(events), events)
	}
	if events[0]["msg"].(sshproxy.WSMessage).Data != "tail" {
		t.Fatalf("expected tail output before closed, got %+v", events[0])
	}
	if events[1]["event"] != "closed" {
		t.Fatalf("expected final closed event, got %+v", events[1])
	}
}
