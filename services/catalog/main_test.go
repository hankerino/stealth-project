package main

import (
	"testing"
	"time"
)

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

func TestValidateFutures(t *testing.T) {
	future := time.Now().UTC().AddDate(0, 2, 0).Format("2006-01-02")
	past := time.Now().UTC().AddDate(0, -1, 0).Format("2006-01-02")
	base := func() futuresInput {
		return futuresInput{GPUTypeID: 1, RegionCode: "us-east-1", DeliveryDate: future, TickSize: 100, ContractSize: 100}
	}
	cases := []struct {
		name    string
		mutate  func(*futuresInput)
		wantErr bool
	}{
		{"valid", func(*futuresInput) {}, false},
		{"missing gpu", func(in *futuresInput) { in.GPUTypeID = 0 }, true},
		{"missing region", func(in *futuresInput) { in.RegionCode = "  " }, true},
		{"bad date", func(in *futuresInput) { in.DeliveryDate = "2026/11/01" }, true},
		{"past date", func(in *futuresInput) { in.DeliveryDate = past }, true},
		{"zero tick", func(in *futuresInput) { in.TickSize = 0 }, true},
		{"negative size", func(in *futuresInput) { in.ContractSize = -1 }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mutate(&in)
			if err := validateFutures(&in); (err != nil) != tc.wantErr {
				t.Fatalf("validateFutures(%+v) err=%v, wantErr=%v", in, err, tc.wantErr)
			}
		})
	}
}

func TestFuturesSymbol(t *testing.T) {
	got := futuresSymbol("H100", "us-east-1", "2026-11-01")
	want := "H100:us-east-1:FUT:2026-11"
	if got != want {
		t.Fatalf("futuresSymbol = %q, want %q", got, want)
	}
}
