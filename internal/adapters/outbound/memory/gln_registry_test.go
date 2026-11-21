package memory_test

import (
	"context"
	"testing"

	"github.com/claudioed/facility-layout/internal/adapters/outbound/memory"
)

func TestGLNRegistryClaim(t *testing.T) {
	registry := memory.NewGLNRegistry()
	ctx := context.Background()
	if ok, err := registry.Claim(ctx, "4006381333931", "site:WH1"); err != nil || !ok {
		t.Fatalf("first claim = %v, %v", ok, err)
	}
	if ok, _ := registry.Claim(ctx, "4006381333931", "slot:WH1-A"); ok {
		t.Fatal("a different owner must not take a held GLN")
	}
	if ok, _ := registry.Claim(ctx, "4006381333931", "site:WH1"); !ok {
		t.Fatal("the holder's repeat claim must succeed")
	}
	if ok, _ := registry.Claim(ctx, "5412345000013", "slot:WH1-A"); !ok {
		t.Fatal("a different GLN is free")
	}
}
