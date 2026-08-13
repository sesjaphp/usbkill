package main

import (
	"context"
	"testing"
	"time"
)

func TestConfigValidationThroughParser(t *testing.T) {
	c := Config{VendorID: "0781", ProductID: "5591", Serial: "abc-123", Grace: 30 * time.Second, ShutdownTimeout: 10 * time.Second, SanitizeTimeout: 2 * time.Second}
	if !idRE.MatchString(c.VendorID) || !idRE.MatchString(c.ProductID) || !serialRE.MatchString(c.Serial) {
		t.Fatal("expected valid identity")
	}
}

func TestMockPoweroffDoesNotRunSystemPoweroff(t *testing.T) {
	if err := (MockPoweroff{}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityMatching(t *testing.T) {
	c := Config{VendorID: "0781", ProductID: "5591", Serial: "abc"}
	if !matches(USB{VendorID: "0781", ProductID: "5591", Serial: "abc"}, c) {
		t.Fatal("expected match")
	}
	if matches(USB{VendorID: "0781", ProductID: "5591", Serial: "other"}, c) {
		t.Fatal("unexpected match")
	}
}
