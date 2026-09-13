package main

// Metrics contract for auth-api (gitops spec 011 T002). The golden-signal
// recording rules and the canary gate read these series, so their names,
// labels, and histogram buckets must survive the move to OpenTelemetry, and
// nothing the specification drops (runtime families, scope labels,
// target_info) may leak into the exposition.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// client_golang v1.24.1's DefBuckets, preserved by spec 011 research R3.
var requestDurationBuckets = []string{"0.005", "0.01", "0.025", "0.05", "0.1", "0.25", "0.5", "1", "2.5", "5", "10", "+Inf"}

var labelPattern = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="([^"]*)"`)

func scrapeAfterTraffic(t *testing.T) string {
	t.Helper()
	e := echo.New()
	e.Use(metricsMiddleware())
	registerOperationalRoutes(e, newHealthState())
	e.GET("/version", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	// A series with labels exports nothing until one combination is observed.
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/version", nil))

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics returned %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

func samples(body, name string) []string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+"{") || strings.HasPrefix(line, name+" ") {
			lines = append(lines, line)
		}
	}
	return lines
}

func labels(line string) map[string]string {
	result := map[string]string{}
	start, end := strings.Index(line, "{"), strings.Index(line, "}")
	if start < 0 || end < start {
		return result
	}
	for _, match := range labelPattern.FindAllStringSubmatch(line[start+1:end], -1) {
		result[match[1]] = match[2]
	}
	return result
}

func labelNames(line string) string {
	var names []string
	for name := range labels(line) {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func TestRequestCounterKeepsItsNameAndLabels(t *testing.T) {
	lines := samples(scrapeAfterTraffic(t), "auth_api_requests_total")
	if len(lines) == 0 {
		t.Fatal("auth_api_requests_total is missing")
	}
	for _, line := range lines {
		if got := labelNames(line); got != "method,status" {
			t.Errorf("auth_api_requests_total labels = %q, want method,status: %s", got, line)
		}
	}
}

func TestRequestDurationHistogramKeepsItsNameLabelsAndBuckets(t *testing.T) {
	body := scrapeAfterTraffic(t)
	var boundaries []string
	for _, line := range samples(body, "auth_api_request_duration_seconds_bucket") {
		if got := labelNames(line); got != "le,method" {
			t.Errorf("bucket labels = %q, want le,method: %s", got, line)
		}
		if labels(line)["method"] == http.MethodGet {
			boundaries = append(boundaries, labels(line)["le"])
		}
	}
	if strings.Join(boundaries, " ") != strings.Join(requestDurationBuckets, " ") {
		t.Errorf("GET bucket boundaries = %v, want %v", boundaries, requestDurationBuckets)
	}
	for _, suffix := range []string{"_sum", "_count"} {
		lines := samples(body, "auth_api_request_duration_seconds"+suffix)
		if len(lines) == 0 {
			t.Errorf("auth_api_request_duration_seconds%s is missing", suffix)
		}
		for _, line := range lines {
			if got := labelNames(line); got != "method" {
				t.Errorf("auth_api_request_duration_seconds%s labels = %q, want method: %s", suffix, got, line)
			}
		}
	}
}

func TestExpositionHasNoScopeLabelsTargetInfoOrRuntimeFamilies(t *testing.T) {
	body := scrapeAfterTraffic(t)
	if strings.Contains(body, "otel_scope_") {
		t.Error("scope labels must be disabled")
	}
	for _, line := range strings.Split(body, "\n") {
		for _, prefix := range []string{"target_info", "go_", "process_", "promhttp_"} {
			if strings.HasPrefix(line, prefix) {
				t.Errorf("the exposition must not contain %s series: %s", prefix, line)
				break
			}
		}
	}
}

func TestNoMetricIsRecordedThroughTheClientGolangAPI(t *testing.T) {
	constructor := regexp.MustCompile(`prometheus\.(New(Counter|Gauge|Histogram|Summary)(Vec)?|MustRegister)\(`)
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if constructor.Match(content) {
			t.Errorf("%s records a metric through client_golang; spec 011 FR-001 requires the OpenTelemetry metrics API", source)
		}
	}
}

// --- business metrics (spec 011 T008) ----------------------------------------

func signInCount(t *testing.T, outcome string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	metricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range samples(rec.Body.String(), "auth_api_sign_ins_total") {
		if labels(line)["outcome"] == outcome {
			value, err := strconv.ParseFloat(line[strings.LastIndex(line, " ")+1:], 64)
			if err != nil {
				t.Fatalf("cannot parse %q: %v", line, err)
			}
			return value
		}
	}
	return 0
}

func runLogin(t *testing.T, service UserService, password string) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"`+password+`"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	_ = getLoginHandler(service)(e.NewContext(req, httptest.NewRecorder()))
}

func usersAPIReturningAdmin() UserService {
	return UserService{
		Client: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"username":"admin","firstname":"Foo","lastname":"Bar","role":"admin"}`), nil
		}),
		UserAPIAddress:    "http://users-api",
		AllowedUserHashes: map[string]interface{}{"admin_admin": nil},
	}
}

func TestAcceptedSignInCountsOnce(t *testing.T) {
	accepted, rejected := signInCount(t, "accepted"), signInCount(t, "rejected")
	runLogin(t, usersAPIReturningAdmin(), "admin")
	if got := signInCount(t, "accepted") - accepted; got != 1 {
		t.Errorf("accepted sign-ins increased by %v, want 1", got)
	}
	if got := signInCount(t, "rejected") - rejected; got != 0 {
		t.Errorf("rejected sign-ins increased by %v, want 0", got)
	}
}

func TestRejectedSignInCountsOnce(t *testing.T) {
	accepted, rejected := signInCount(t, "accepted"), signInCount(t, "rejected")
	runLogin(t, usersAPIReturningAdmin(), "wrong")
	if got := signInCount(t, "rejected") - rejected; got != 1 {
		t.Errorf("rejected sign-ins increased by %v, want 1", got)
	}
	if got := signInCount(t, "accepted") - accepted; got != 0 {
		t.Errorf("accepted sign-ins increased by %v, want 0", got)
	}
}

func TestSignInFailingOnAServerErrorCountsNeitherOutcome(t *testing.T) {
	accepted, rejected := signInCount(t, "accepted"), signInCount(t, "rejected")
	unavailable := UserService{
		Client: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("users-api unavailable")
		}),
		UserAPIAddress:    "http://users-api",
		AllowedUserHashes: map[string]interface{}{"admin_admin": nil},
	}
	runLogin(t, unavailable, "admin")
	if signInCount(t, "accepted") != accepted || signInCount(t, "rejected") != rejected {
		t.Error("a sign-in failing on a server error must not count as accepted or rejected")
	}
}

func TestSignInSeriesCarriesOnlyTheOutcomeLabel(t *testing.T) {
	runLogin(t, usersAPIReturningAdmin(), "admin")
	runLogin(t, usersAPIReturningAdmin(), "wrong")
	rec := httptest.NewRecorder()
	metricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	lines := samples(rec.Body.String(), "auth_api_sign_ins_total")
	if len(lines) == 0 {
		t.Fatal("auth_api_sign_ins_total is missing")
	}
	for _, line := range lines {
		if got := labelNames(line); got != "outcome" {
			t.Errorf("auth_api_sign_ins_total labels = %q, want outcome: %s", got, line)
		}
	}
}
