package hub

import (
	"strings"
	"testing"
	"time"
)

func TestRequestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := LoadRequest(dir); ok {
		t.Fatal("sem arquivo não há pedido")
	}
	at := time.Now().Truncate(time.Second)
	if err := SaveRequest(dir, Request{Asked: at, Pending: true}); err != nil {
		t.Fatal(err)
	}
	r, ok := LoadRequest(dir)
	if !ok || !r.Asked.Equal(at) || !r.Pending {
		t.Fatalf("pedido = %+v %v", r, ok)
	}
}

func TestTrustKeys(t *testing.T) {
	screen := " Quick safety check: Is this a project you created or one you trust?\n ❯ No, exit\n   Yes, I trust this folder\n Enter to confirm"
	if k, ok := TrustKeys(screen); !ok || strings.Join(k, " ") != "down enter" {
		t.Fatalf("keys = %v %v", k, ok)
	}
	screen = " ❯ 1. Yes, I trust this folder\n   2. No, exit"
	if k, ok := TrustKeys(screen); !ok || strings.Join(k, " ") != "enter" {
		t.Fatalf("keys = %v %v", k, ok)
	}
	if _, ok := TrustKeys("Do you want to make this edit?\n ❯ 1. Yes\n   2. No"); ok {
		t.Fatal("pedido de permissão comum não é o de confiança")
	}
}
