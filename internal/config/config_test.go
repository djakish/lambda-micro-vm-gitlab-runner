package config

import "testing"

type fakeEnv map[string]string

func (f fakeEnv) get(k string) string { return f[k] }

func TestLoadDefaults(t *testing.T) {
	cfg, err := loadFrom(fakeEnv{
		"MICROVM_REGION":    "eu-west-1",
		"MICROVM_IMAGE_ARN": "arn:aws:lambda:eu-west-1:123:microvm-image:ci",
	}.get)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.AgentPort != 8080 {
		t.Errorf("AgentPort = %d, want 8080", cfg.AgentPort)
	}
	if cfg.BuildsDir != "/builds" || cfg.CacheDir != "/cache" {
		t.Errorf("builds/cache = %q/%q", cfg.BuildsDir, cfg.CacheDir)
	}
	wantIngress := "arn:aws:lambda:eu-west-1:aws:network-connector:aws-network-connector:ALL_INGRESS"
	if len(cfg.IngressConnectors) != 1 || cfg.IngressConnectors[0] != wantIngress {
		t.Errorf("IngressConnectors = %v, want [%s]", cfg.IngressConnectors, wantIngress)
	}
	wantEgress := "arn:aws:lambda:eu-west-1:aws:network-connector:aws-network-connector:INTERNET_EGRESS"
	if len(cfg.EgressConnectors) != 1 || cfg.EgressConnectors[0] != wantEgress {
		t.Errorf("EgressConnectors = %v, want [%s]", cfg.EgressConnectors, wantEgress)
	}
	if cfg.AWSCLI != "aws" {
		t.Errorf("AWSCLI = %q, want aws", cfg.AWSCLI)
	}
}

func TestLoadRequiresRegionAndImage(t *testing.T) {
	if _, err := loadFrom(fakeEnv{"MICROVM_IMAGE_ARN": "x"}.get); err == nil {
		t.Error("expected error when region is unset")
	}
	if _, err := loadFrom(fakeEnv{"MICROVM_REGION": "eu-west-1"}.get); err == nil {
		t.Error("expected error when image is unset")
	}
}

func TestJobImageOverride(t *testing.T) {
	cfg, err := loadFrom(fakeEnv{
		"AWS_REGION":               "us-east-1",
		"MICROVM_IMAGE_ARN":        "runner-default",
		"CUSTOM_ENV_MICROVM_IMAGE": "job-override",
	}.get)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.ImageARN != "job-override" {
		t.Errorf("ImageARN = %q, want job-override", cfg.ImageARN)
	}
}

func TestMultipleConnectors(t *testing.T) {
	cfg, err := loadFrom(fakeEnv{
		"MICROVM_REGION":            "us-east-1",
		"MICROVM_IMAGE_ARN":         "img",
		"MICROVM_EGRESS_CONNECTORS": "arn:a, arn:b ,arn:c",
	}.get)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if len(cfg.EgressConnectors) != 3 {
		t.Fatalf("EgressConnectors = %v, want 3 entries", cfg.EgressConnectors)
	}
}

func TestTokenTTLFromJobTimeout(t *testing.T) {
	cfg, err := loadFrom(fakeEnv{
		"MICROVM_REGION":            "us-east-1",
		"MICROVM_IMAGE_ARN":         "img",
		"CUSTOM_ENV_CI_JOB_TIMEOUT": "3600",
	}.get)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.TokenTTLMinutes != 70 { // 3600/60 + 10
		t.Errorf("TokenTTLMinutes = %d, want 70", cfg.TokenTTLMinutes)
	}
}
