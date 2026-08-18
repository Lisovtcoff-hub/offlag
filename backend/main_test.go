package main

import "testing"

func TestNormEmail(t *testing.T) {
	got := normEmail("  User@Example.COM  ")
	if got != "user@example.com" {
		t.Fatalf("normEmail() = %q", got)
	}
}

func TestClampDeviceName(t *testing.T) {
	got := clampDeviceName("  Windows laptop  ")
	if got != "Windows laptop" {
		t.Fatalf("clampDeviceName() = %q", got)
	}
}

func TestHashTokenIsDeterministic(t *testing.T) {
	first := hashToken("refresh-token")
	second := hashToken("refresh-token")
	if first == "" || first != second {
		t.Fatalf("hashToken() returned inconsistent values")
	}
}
