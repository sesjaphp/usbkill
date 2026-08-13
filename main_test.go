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

func TestRemovalMatchesUSBAndBlockEvents(t *testing.T) {
	c := Config{VendorID: "0204", ProductID: "6025", Serial: "047894467501"}
	base := map[string]string{"ACTION": "remove", "ID_BUS": "usb", "ID_VENDOR_ID": "0204", "ID_MODEL_ID": "6025", "ID_SERIAL_SHORT": "047894467501"}
	if !removalMatches(base, c) {
		t.Fatal("expected USB removal match")
	}
	base["ID_SERIAL_SHORT"] = "0204_6025_047894467501"
	if !removalMatches(base, c) {
		t.Fatal("expected normalized serial match")
	}
	base["ID_BUS"] = "pci"
	if removalMatches(base, c) {
		t.Fatal("unexpected unrelated bus match")
	}
}
