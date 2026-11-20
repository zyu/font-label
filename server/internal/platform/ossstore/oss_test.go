package ossstore

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

func TestInternalOSSConfigurationUsesCanonicalObjectURL(t *testing.T) {
	t.Setenv("OSS_ACCESS_KEY_ID", "fixture-access-key")
	t.Setenv("OSS_ACCESS_KEY_SECRET", "fixture-not-a-real-secret")
	cfg := Config{
		Bucket: "fixture", Region: "cn-chengdu",
		Endpoint: "https://oss-cn-chengdu-internal.aliyuncs.com", Prefix: "/example/exports/",
	}
	store, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	key := store.NewObjectKey()
	if !strings.HasPrefix(key, "example/exports/") || !strings.HasSuffix(key, ".zip") || key == store.NewObjectKey() {
		t.Fatalf("unexpected object key: %q", key)
	}
	if got := store.ObjectURL(key); got != "https://fixture.oss-cn-chengdu.aliyuncs.com/"+key {
		t.Fatalf("unexpected object URL: %s", got)
	}
	cfg.Endpoint = "https://oss-cn-other.aliyuncs.com"
	if _, err := New(cfg); err == nil {
		t.Fatal("accepted an endpoint for a different region")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestUploadMakesSignedZIPRequestAndReturnsSafeErrors(t *testing.T) {
	t.Setenv("OSS_ACCESS_KEY_ID", "fixture-access-key")
	t.Setenv("OSS_ACCESS_KEY_SECRET", "fixture-not-a-real-secret")
	file := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(file, []byte("zip fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	var deny bool
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if r.Method != http.MethodPut || r.URL.Host != "fixture.oss-cn-chengdu.aliyuncs.com" ||
			r.URL.Path != "/exports/sample.zip" || r.Header.Get("Content-Type") != "application/zip" ||
			!strings.HasPrefix(r.Header.Get("Authorization"), "OSS4-HMAC-SHA256 ") || string(body) != "zip fixture" {
			t.Errorf("unexpected OSS upload request: %s %s", r.Method, r.URL)
		}
		res := &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody, Request: r}
		if deny {
			res.StatusCode = http.StatusForbidden
			res.Body = io.NopCloser(strings.NewReader("<Error><Code>AccessDenied</Code><Message>opaque provider details</Message></Error>"))
		}
		return res, nil
	})}
	cfg := oss.LoadDefaultConfig().WithRegion("cn-chengdu").
		WithEndpoint("https://oss-cn-chengdu.aliyuncs.com").
		WithCredentialsProvider(credentials.NewEnvironmentVariableCredentialsProvider()).
		WithHttpClient(httpClient)
	store := &Store{bucket: "fixture", uploader: oss.NewClient(cfg).NewUploader()}
	if err := store.Upload(context.Background(), "exports/sample.zip", file); err != nil {
		t.Fatal(err)
	}
	deny = true
	err := store.Upload(context.Background(), "exports/sample.zip", file)
	if err == nil || err.Error() != "OSS 上传失败（AccessDenied）" {
		t.Fatalf("unexpected upload failure: %v", err)
	}
}
