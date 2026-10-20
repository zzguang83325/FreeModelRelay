package loomy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStringToSignNineLines(t *testing.T) {
	sts := stringToSign("POST", "/login/account/getPuKey", "", `{"base":{}}`,
		"application/json", "Sat, 26 Sep 2026 12:00:00 GMT", "nonce-1", "", "")
	got := strings.Split(sts, "\n")
	if len(got) != 9 {
		t.Fatalf("stringToSign 必须是 9 行，得到 %d 行: %q", len(got), sts)
	}
	want := []string{
		"POST",
		"/login/account/getPuKey",
		"",
		contentMD5(`{"base":{}}`),
		"application/json",
		"Sat, 26 Sep 2026 12:00:00 GMT",
		"nonce-1",
		"",
		"",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 行 = %q, 期望 %q", i+1, got[i], want[i])
		}
	}
}

func TestEscapeRFC3986(t *testing.T) {
	cases := map[string]string{
		"abc-_.~123": "abc-_.~123",
		"a b":        "a%20b",
		"a/b":        "a%2Fb",
		"a+b":        "a%2Bb",
		"! '() *":    "%21%20%27%28%29%20%2A",
		"中文":         "%E4%B8%AD%E6%96%87",
	}
	for in, want := range cases {
		if got := escapeRFC3986(in); got != want {
			t.Fatalf("escapeRFC3986(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestEscapedPathKeepsSlashes(t *testing.T) {
	if got := escapedPath("login/account/getPuKey"); got != "/login/account/getPuKey" {
		t.Fatalf("escapedPath = %q", got)
	}
	if got := escapedPath("/a b/c/"); got != "/a%20b/c" {
		t.Fatalf("escapedPath = %q", got)
	}
}

func TestSignAuthKnownVector(t *testing.T) {
	// HMAC-SHA1(key="secret", msg="hello") 的固定结果，防止签名实现漂移。
	got := signAuth("secret", "hello")
	want := "URIFXAX5RPhXVe/FzYlw4ZTp9Fs="
	if got != want {
		t.Fatalf("signAuth = %q, 期望 %q", got, want)
	}
}

func TestBuildAccountHeadersShape(t *testing.T) {
	h := buildAccountHeaders("id-x", "sec-y", http.MethodPost, "/p", `{"a":1}`)
	if !strings.HasPrefix(h.Get("Authorization"), "account id-x:") {
		t.Fatalf("Authorization = %q", h.Get("Authorization"))
	}
	if h.Get("Content-MD5") == "" {
		t.Fatal("非空 body 必须带 Content-MD5")
	}
	if h.Get("Date") == "" || h.Get("Nonce") == "" {
		t.Fatal("缺少 Date/Nonce 头")
	}
	empty := buildAccountHeaders("id-x", "sec-y", http.MethodGet, "/p", "")
	if empty.Get("Content-MD5") != "" {
		t.Fatal("空 body 不应带 Content-MD5")
	}
}

func TestLoginByPasswordFakeUpstream(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pukey := base64.StdEncoding.EncodeToString(der)

	var sawAuth, sawMD5 string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAll(t, r)
		sawAuth = r.Header.Get("Authorization")
		sawMD5 = r.Header.Get("Content-MD5")
		if !strings.HasPrefix(sawAuth, "account "+DefaultAccessKeyID+":") {
			t.Errorf("Authorization 格式错误: %q", sawAuth)
		}
		switch r.URL.Path {
		case "/login/account/getPuKey":
			writeJSON(t, w, map[string]any{
				"code": "000000",
				"data": map[string]any{"pukey": pukey, "rcode": "abc123"},
			})
		case "/login/account/byPwd":
			var env struct {
				Base  map[string]any `json:"base"`
				Param map[string]any `json:"param"`
			}
			if err := json.Unmarshal([]byte(body), &env); err != nil {
				t.Fatalf("bad envelope: %v", err)
			}
			if env.Param["rcode"] != "abc123" {
				t.Errorf("rcode 未透传: %v", env.Param)
			}
			if env.Param["expire"] != float64(SessionExpireSeconds) {
				t.Errorf("expire 错误: %v", env.Param["expire"])
			}
			if env.Base["appid"] != DefaultAppID || env.Base["devid"] == "" || env.Base["traceid"] == "" {
				t.Errorf("信封 base 不完整: %v", env.Base)
			}
			writeJSON(t, w, map[string]any{
				"code": "000000",
				"data": map[string]any{"session": "sess-1", "userid": "u9", "phone": "13800000000"},
			})
		default:
			t.Errorf("意外路径: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.Client())
	c.AccountBase = srv.URL
	c.HTTP = srv.Client()

	ident := NewIdentity()
	sess, err := c.LoginByPassword("13800000000", "hunter2", ident)
	if err != nil {
		t.Fatalf("LoginByPassword: %v", err)
	}
	if sess.Session != "sess-1" || sess.UserID != "u9" || sess.Phone != "13800000000" {
		t.Fatalf("session 解析错误: %+v", sess)
	}
	if sawMD5 == "" {
		t.Error("账号服务请求缺少 Content-MD5")
	}
	if !strings.HasPrefix(ident.Devid, "web-") || len(ident.Devid) != 4+16 {
		t.Fatalf("per-account devid 形态错误: %q", ident.Devid)
	}
}

func TestLoginByPasswordHandlesMissingB64Padding(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 1024)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pukey := base64.RawStdEncoding.EncodeToString(der) // 无 padding

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/account/getPuKey" {
			writeJSON(t, w, map[string]any{"code": "000000", "data": map[string]any{"pukey": pukey, "rcode": "r"}})
			return
		}
		writeJSON(t, w, map[string]any{"code": "000000", "data": map[string]any{"session": "s"}})
	}))
	defer srv.Close()

	c := NewClient(nil)
	c.AccountBase = srv.URL
	c.HTTP = srv.Client()
	if _, err := c.LoginByPassword("x", "y", NewIdentity()); err != nil {
		t.Fatalf("无 padding 公钥应可用: %v", err)
	}
}

func TestAccountBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"code": "100001", "desc": "验证码错误"})
	}))
	defer srv.Close()
	c := NewClient(nil)
	c.AccountBase = srv.URL
	c.HTTP = srv.Client()
	_, err := c.LoginBySMS("138", "1234", "mid", NewIdentity())
	e, ok := AsError(err)
	if !ok || !e.Retryable || e.Code != "100001" {
		t.Fatalf("业务错误分类错误: %#v", err)
	}
}

func TestParseModelListShapes(t *testing.T) {
	one := `[{"id":"spark-x","name":"Spark X2.5 (x0.1)"}]`
	two := `{"data":[{"id":"a"}]}`
	three := `{"data":{"data":[{"id":"b"}]}}`
	for name, raw := range map[string]string{"array": one, "envelope": two, "nested": three} {
		list, err := parseModelList([]byte(raw))
		if err != nil || len(list) != 1 {
			t.Fatalf("%s 形态解析失败: %v %v", name, list, err)
		}
	}
	if _, err := parseModelList([]byte(`{"err":"x"}`)); err == nil {
		t.Fatal("坏响应应报错")
	}
}

func TestFetchPoints(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Header.Get("traceparent") == "" || r.Header.Get("loomy-version") != DefaultLoomyVersion {
			t.Error("网关请求缺少 traceparent / loomy-version")
		}
		writeJSON(t, w, map[string]any{"code": "000000", "data": map[string]any{
			"balance": 100.5, "dailyBalance": 50, "availableBalance": 150.5,
		}})
	}))
	defer srv.Close()
	c := NewClient(nil)
	c.ModelBase = srv.URL
	c.HTTP = srv.Client()
	p, err := c.FetchPoints("sess-9")
	if err != nil {
		t.Fatal(err)
	}
	if p.Balance != 100.5 || p.DailyBalance != 50 || p.AvailableBalance != 150.5 {
		t.Fatalf("点数解析错误: %+v", p)
	}
	if gotAuth != "Bearer sess-9" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestNewIdentityShape(t *testing.T) {
	id := NewIdentity()
	if !strings.HasPrefix(id.CampusDevice, CampusPrefix) {
		t.Fatalf("campus device id = %q", id.CampusDevice)
	}
	if id.UA != ClientUA || id.ModelID != WebModelID || id.Version != ClientVer {
		t.Fatalf("身份未镜像真实客户端: %+v", id)
	}
}

// ---- helpers ----

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func readAll(t *testing.T, r *http.Request) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}
