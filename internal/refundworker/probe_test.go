package refundworker_test

import (
	"context"
	"encoding/json"
	"llm-refund/internal/refundworker"
	"llm-refund/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func probeRunner(t *testing.T, source string) refundworker.ProtocolRunner {
	t.Helper()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("Python unavailable")
	}
	path := filepath.Join(t.TempDir(), "probe.py")
	if err = os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return refundworker.ProtocolRunner{Python: python, Script: path}
}
func TestProbeSanitizesEveryOutputField(t *testing.T) {
	p := probeRunner(t, `import json,sys
job=json.load(sys.stdin)
print(json.dumps({'valid':True,'code':'ok','http_status':200,'latency_ms':-999,'checked_at':'invalid','message':'private-token','capacity_message':'backend-api/private','seats':{'standard':{'total':8,'remaining':3,'held':2,'used':5},'premium':{'total':-1}},'ip_address':'private-token','headers':'private-token'}))`)
	// Invalid values cannot bypass parsing into the response shape.
	r, err := p.ProbeMother(context.Background(), &store.RefundJob{})
	if err != nil || r.Valid {
		t.Fatal("invalid wire timestamp was accepted", err)
	}
	p = probeRunner(t, `import json,sys
json.load(sys.stdin)
print(json.dumps({'valid':True,'code':'ok','http_status':200,'message':'private-token','capacity_message':'backend-api/private','seats':{'standard':{'total':8,'remaining':3,'held':2,'used':5},'premium':{'total':-1}},'ip_address':'private-token'}))`)
	r, err = p.ProbeMother(context.Background(), &store.RefundJob{})
	if err != nil || !r.Valid || r.Seats == nil || r.Seats.Standard.Remaining != 3 || r.Seats.Premium != nil {
		t.Fatal("normalized seats missing", err)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "private") || r.CheckedAt.IsZero() || r.LatencyMS < 0 {
		t.Fatal("unsafe probe output")
	}
	r, err = p.ProbeProxy(context.Background(), "http://user:password@proxy.invalid")
	if err != nil || r.Valid || r.IPAddress != "" {
		t.Fatal("invalid IP accepted")
	}
}
func TestProbeTimeoutAndSecretErrorsAreSafe(t *testing.T) {
	p := probeRunner(t, `import time;time.sleep(30)`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r, err := p.ProbeProxy(ctx, "http://secret.invalid")
	if err != nil || r.Valid || r.Code != "timeout" {
		t.Fatal("timeout not classified", err)
	}
	p = probeRunner(t, `import json;print(json.dumps({'valid':False,'http_status':0,'code':'tls_config','message':'secret-upstream-url'}))`)
	r, err = p.ProbeMother(context.Background(), &store.RefundJob{})
	if err != nil || r.Code != "tls_config" || strings.Contains(r.Message, "secret") {
		t.Fatal("unsafe TLS error")
	}
}
