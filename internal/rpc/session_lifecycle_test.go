package rpc

import (
	"context"
	"testing"
	"time"
)

func TestServerStopsAllSessions(t *testing.T) {
	server := NewServer(DefaultConfig())
	c1, s1 := testYamuxPair(t)
	defer c1.Close()
	c2, s2 := testYamuxPair(t)
	defer c2.Close()
	done := make(chan struct{}, 2)
	go func() { server.ServeSession(context.Background(), s1); done <- struct{}{} }()
	go func() { server.ServeSession(context.Background(), s2); done <- struct{}{} }()
	deadline := time.Now().Add(time.Second)
	for {
		server.mu.RLock()
		count := len(server.sessions)
		server.mu.RUnlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sessions did not start")
		}
		time.Sleep(time.Millisecond)
	}
	stopped := make(chan struct{})
	go func() { server.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not close all sessions")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("ServeSession remained blocked")
		}
	}
}
