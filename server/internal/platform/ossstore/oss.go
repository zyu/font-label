package ossstore

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

type Config struct {
	Bucket   string
	Region   string
	Endpoint string
	Prefix   string
}

type Store struct {
	bucket     string
	prefix     string
	publicHost string
	uploader   *oss.Uploader
}

var (
	bucketName   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	regionID     = regexp.MustCompile(`^[a-z]{2}-[a-z0-9-]{2,40}$`)
	objectPrefix = regexp.MustCompile(`^[a-zA-Z0-9_-]+(/[a-zA-Z0-9_-]+)*$`)
	serviceCode  = regexp.MustCompile(`^[a-zA-Z0-9]{1,64}$`)
)

// New checks the deployment configuration without contacting the bucket.
func New(cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, nil
	}
	if !bucketName.MatchString(cfg.Bucket) || !regionID.MatchString(cfg.Region) {
		return nil, errors.New("OSS_BUCKET 或 OSS_REGION 无效")
	}
	host := "oss-" + cfg.Region + ".aliyuncs.com"
	internalHost := "oss-" + cfg.Region + "-internal.aliyuncs.com"
	endpoint := strings.TrimSuffix(cfg.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://" + host
	}
	if endpoint != "https://"+host && endpoint != "https://"+internalHost {
		return nil, errors.New("OSS_ENDPOINT 必须是所配地域的 OSS HTTPS Endpoint")
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix == "" {
		prefix = "font-label/exports"
	}
	if len(prefix) > 900 || !objectPrefix.MatchString(prefix) {
		return nil, errors.New("OSS_EXPORT_PREFIX 无效")
	}
	provider := credentials.NewEnvironmentVariableCredentialsProvider()
	if _, err := provider.GetCredentials(context.Background()); err != nil {
		return nil, errors.New("请配置 OSS_ACCESS_KEY_ID 和 OSS_ACCESS_KEY_SECRET")
	}
	client := oss.NewClient(oss.LoadDefaultConfig().
		WithRegion(cfg.Region).WithEndpoint(endpoint).WithCredentialsProvider(provider).
		WithReadWriteTimeout(2 * time.Minute))
	uploader := client.NewUploader(func(o *oss.UploaderOptions) {
		o.PartSize = 64 * 1024 * 1024
	})
	return &Store{bucket: cfg.Bucket, prefix: prefix, publicHost: cfg.Bucket + "." + host, uploader: uploader}, nil
}

func (s *Store) NewObjectKey() string {
	return path.Join(s.prefix, time.Now().UTC().Format("2006/01/02"), "annotations-"+rand.Text()+".zip")
}

func (s *Store) ObjectURL(key string) string {
	return (&url.URL{Scheme: "https", Host: s.publicHost, Path: "/" + key}).String()
}

func (s *Store) Upload(ctx context.Context, key, filename string) error {
	_, err := s.uploader.UploadFile(ctx, &oss.PutObjectRequest{
		Bucket: oss.Ptr(s.bucket), Key: oss.Ptr(key),
		ContentType: oss.Ptr("application/zip"),
	}, filename)
	if err == nil {
		return nil
	}
	// Provider errors may contain a request dump. Persist only the service code.
	var service *oss.ServiceError
	if errors.As(err, &service) && serviceCode.MatchString(service.Code) {
		return fmt.Errorf("OSS 上传失败（%s）", service.Code)
	}
	return errors.New("OSS 上传失败，请检查连通性、地域和上传权限")
}
