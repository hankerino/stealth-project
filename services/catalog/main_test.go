package main

import "testing"

func TestValidateGPUType(t *testing.T) {
	cases := []struct {
		name    string
		gpuName string
		vramGB  int
		wantErr bool
	}{
		{"valid H100", "H100", 80, false},
		{"valid B200", "B200", 192, false},
		{"empty name", "", 80, true},
		{"blank name", "   ", 80, true},
		{"zero vram", "H100", 0, true},
		{"negative vram", "H100", -8, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGPUType(tc.gpuName, tc.vramGB)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateGPUType(%q, %d) err=%v, wantErr=%v",
					tc.gpuName, tc.vramGB, err, tc.wantErr)
			}
		})
	}
}

func TestValidateRegion(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		regName string
		wantErr bool
	}{
		{"valid", "us-east-1", "US East (N. Virginia)", false},
		{"empty code", "", "US East", true},
		{"empty name", "us-east-1", "", true},
		{"both empty", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRegion(tc.code, tc.regName)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateRegion(%q, %q) err=%v, wantErr=%v",
					tc.code, tc.regName, err, tc.wantErr)
			}
		})
	}
}
