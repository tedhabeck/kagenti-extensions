package edit

import (
	"strings"
	"testing"
)

const fixtureMidYAML = `mode: proxy-sidecar

listener:
  forward_proxy_addr: ":8081"

pipeline:
  inbound:
    - name: jwt-validation
      config:
        issuer: http://idp
  outbound:
    - name: token-exchange

session:
  enabled: true
`

const fixtureLastYAML = `mode: proxy-sidecar

pipeline:
  inbound:
    - name: jwt-validation
`

const fixtureFirstYAML = `pipeline:
  inbound:
    - name: jwt-validation

mode: proxy-sidecar
`

const fixtureMissingYAML = `mode: proxy-sidecar

listener:
  forward_proxy_addr: ":8081"
`

const fixtureCMYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: authbridge-config-email-agent
  namespace: team1
data:
  config.yaml: |
    mode: proxy-sidecar
    pipeline:
      inbound:
        - name: jwt-validation
          config:
            issuer: old
    session:
      enabled: true
`

func TestFindPipelineRange_Middle(t *testing.T) {
	start, end, err := FindPipelineRange([]byte(fixtureMidYAML))
	if err != nil {
		t.Fatalf("FindPipelineRange: %v", err)
	}
	got := fixtureMidYAML[start:end]
	if !strings.Contains(got, "pipeline:") {
		t.Fatalf("range missing pipeline header: %q", got)
	}
	if !strings.Contains(got, "token-exchange") {
		t.Fatalf("range missing pipeline body: %q", got)
	}
	if strings.Contains(got, "session:") {
		t.Fatalf("range includes next key: %q", got)
	}
	if strings.Contains(got, "listener:") {
		t.Fatalf("range includes prior key: %q", got)
	}
}

func TestFindPipelineRange_LastKey(t *testing.T) {
	start, end, err := FindPipelineRange([]byte(fixtureLastYAML))
	if err != nil {
		t.Fatalf("FindPipelineRange: %v", err)
	}
	if end != len(fixtureLastYAML) {
		t.Fatalf("end = %d, want len(yaml) = %d", end, len(fixtureLastYAML))
	}
	got := fixtureLastYAML[start:end]
	if !strings.Contains(got, "jwt-validation") {
		t.Fatalf("range missing pipeline body: %q", got)
	}
}

func TestFindPipelineRange_FirstKey(t *testing.T) {
	start, _, err := FindPipelineRange([]byte(fixtureFirstYAML))
	if err != nil {
		t.Fatalf("FindPipelineRange: %v", err)
	}
	if start != 0 {
		t.Fatalf("start = %d, want 0", start)
	}
}

func TestFindPipelineRange_Missing(t *testing.T) {
	_, _, err := FindPipelineRange([]byte(fixtureMissingYAML))
	if err == nil {
		t.Fatal("want error when pipeline key is absent")
	}
	if !strings.Contains(err.Error(), "pipeline") {
		t.Fatalf("error should mention pipeline: %v", err)
	}
}

func TestSplice_PreservesOutsideRange(t *testing.T) {
	const orig = `mode: proxy-sidecar
# this comment must survive
listener:
  forward_proxy_addr: ":8081"

pipeline:
  inbound:
    - name: jwt-validation

session:
  enabled: true
`
	start, end, err := FindPipelineRange([]byte(orig))
	if err != nil {
		t.Fatal(err)
	}
	const newSubtree = `pipeline:
  inbound:
    - name: jwt-validation
      config:
        issuer: new

`
	got := Splice([]byte(orig), start, end, []byte(newSubtree))
	gotS := string(got)
	if !strings.Contains(gotS, "# this comment must survive") {
		t.Fatalf("comment outside pipeline subtree was dropped:\n%s", gotS)
	}
	if !strings.Contains(gotS, "listener:") {
		t.Fatalf("listener section was dropped:\n%s", gotS)
	}
	if !strings.Contains(gotS, "session:") {
		t.Fatalf("session section was dropped:\n%s", gotS)
	}
	if !strings.Contains(gotS, "issuer: new") {
		t.Fatalf("new pipeline content not present:\n%s", gotS)
	}
	if strings.Contains(gotS, "issuer: old") {
		t.Fatalf("old pipeline content still present:\n%s", gotS)
	}
}

func TestBuildManifest_UpdatesDataField(t *testing.T) {
	const newInner = `mode: proxy-sidecar
pipeline:
  inbound:
    - name: jwt-validation
      config:
        issuer: new
session:
  enabled: true
`
	out, err := BuildManifest([]byte(fixtureCMYAML), []byte(newInner))
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	outS := string(out)
	if !strings.Contains(outS, "name: authbridge-config-email-agent") {
		t.Fatalf("metadata.name lost:\n%s", outS)
	}
	if !strings.Contains(outS, "namespace: team1") {
		t.Fatalf("metadata.namespace lost:\n%s", outS)
	}
	if !strings.Contains(outS, "issuer: new") {
		t.Fatalf("new content not in manifest:\n%s", outS)
	}
	if strings.Contains(outS, "issuer: old") {
		t.Fatalf("old content still in manifest:\n%s", outS)
	}
}
