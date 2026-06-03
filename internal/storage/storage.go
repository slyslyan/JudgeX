package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ============================================================================
// 测试数据存储层 — 本地磁盘 / S3 双后端
// ============================================================================
//
// storage 包抽象了测试用例文件的存储方式，支持两种后端：
//
//   1. LocalBackend（本地磁盘）— 默认后端，文件存储在 TEST_DATA_PATH 目录下
//      目录结构：{BasePath}/{problemID}/{num}.in / {num}.out
//
//   2. S3Backend（S3 兼容存储）— 通过环境变量 S3_ENDPOINT 启用
//      支持 MinIO、AWS S3、阿里云 OSS 等所有 S3 兼容对象存储
//      目录结构：testcases/{problemID}/{num}.in / {num}.out
//
// 测试文件以数字编号配对存储（如 1.in / 1.out, 2.in / 2.out），
// 这种格式比单文件 JSON/ZIP 更方便逐条读取。
//
// 判断逻辑：如果设置了 S3_ENDPOINT 环境变量使用 S3，否则用本地磁盘。
// 如果 S3 初始化失败，自动降级到本地磁盘。
//
// Default 是全局存储后端实例，在 Init() 中确定。

// Backend 是测试用例存储的抽象接口。
// 支持四种操作：保存、读取、列表、删除。
// 无论是本地磁盘还是 S3，都实现这个接口。
type Backend interface {
	// SaveTestCases 保存题目的所有测试用例文件。
	// files 参数是文件名 → 文件内容的映射（如 {"1.in": data, "1.out": data}）。
	SaveTestCases(problemID uint, files map[string][]byte) error

	// ReadTestCase 读取单个测试用例文件。
	// filename 是相对路径（如 "1.in"）。
	ReadTestCase(problemID uint, filename string) ([]byte, error)

	// ListTestCases 列出题目的所有测试用例文件。
	// 返回文件名和大小列表。
	ListTestCases(problemID uint) ([]FileInfo, error)

	// DeleteTestCases 删除题目的所有测试用例文件。
	DeleteTestCases(problemID uint) error
}

// FileInfo 表示存储中的一个文件信息。
type FileInfo struct {
	Name string // 文件名（如 "1.in"）
	Size int64  // 文件大小（字节）
}

// Default 是全局的存储后端实例。
// 其他包通过 storage.Default 访问，无需关心底层是本地磁盘还是 S3。
var Default Backend

// Init 根据环境变量初始化存储后端。
// 优先使用 S3（如果配置了 S3_ENDPOINT），否则使用本地磁盘。
func Init() {
	endpoint := os.Getenv("S3_ENDPOINT")
	if endpoint == "" {
		// 使用本地磁盘
		path := os.Getenv("TEST_DATA_PATH")
		if path == "" {
			path = "/home/sly/Downloads/oj/data/testcases"
		}
		Default = &LocalBackend{BasePath: path}
		log.Println("[storage] using local disk backend")
		return
	}

	// 使用 S3 兼容存储
	accessKey := os.Getenv("S3_ACCESS_KEY")
	secretKey := os.Getenv("S3_SECRET_KEY")
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		bucket = "judgex-testcases"
	}
	useSSL := os.Getenv("S3_USE_SSL") != "false" // 默认启用 SSL

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		log.Printf("[storage] MinIO init failed: %v, falling back to local disk", err)
		path := os.Getenv("TEST_DATA_PATH")
		if path == "" {
			path = "/home/sly/Downloads/oj/data/testcases"
		}
		Default = &LocalBackend{BasePath: path}
		return
	}

	// 确保 bucket 存在（不存在则创建）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		log.Printf("[storage] S3 bucket check failed: %v", err)
	} else if !exists {
		client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: "us-east-1"})
		log.Printf("[storage] created S3 bucket: %s", bucket)
	}

	Default = &S3Backend{client: client, bucket: bucket}
	log.Printf("[storage] using S3 backend (endpoint=%s, bucket=%s)", endpoint, bucket)
}

// ============================================================================
// LocalBackend — 本地磁盘存储
// ============================================================================

// LocalBackend 将测试用例存储在本地文件系统。
// 目录结构：{BasePath}/{problemID}/
type LocalBackend struct {
	BasePath string // 测试数据的根目录
}

// dir 返回题目测试数据的目录路径。
func (b *LocalBackend) dir(problemID uint) string {
	return filepath.Join(b.BasePath, fmt.Sprintf("%d", problemID))
}

// SaveTestCases 保存测试用例到本地磁盘。
// 先删除旧目录，再创建新目录并写入所有文件。
func (b *LocalBackend) SaveTestCases(problemID uint, files map[string][]byte) error {
	dir := b.dir(problemID)
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0644); err != nil {
			return err
		}
	}
	return nil
}

// ReadTestCase 从本地磁盘读取单个测试用例文件。
func (b *LocalBackend) ReadTestCase(problemID uint, filename string) ([]byte, error) {
	return os.ReadFile(filepath.Join(b.dir(problemID), filename))
}

// ListTestCases 列出题目在本地磁盘上的所有测试用例文件。
func (b *LocalBackend) ListTestCases(problemID uint) ([]FileInfo, error) {
	dir := b.dir(problemID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var infos []FileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		infos = append(infos, FileInfo{Name: e.Name(), Size: size})
	}
	return infos, nil
}

// DeleteTestCases 删除题目在本地磁盘上的所有测试用例文件。
func (b *LocalBackend) DeleteTestCases(problemID uint) error {
	return os.RemoveAll(b.dir(problemID))
}

// ============================================================================
// S3Backend — S3 兼容对象存储
// ============================================================================
//
// S3 存储的路径结构（在 bucket 内）：
//   testcases/{problemID}/{filename}
//
// 这种前缀结构将所有题目的测试数据组织在同一 bucket 下，
// 每个题目一个前缀（类似目录）。

// S3Backend 将测试用例存储在 S3 兼容的对象存储中。
type S3Backend struct {
	client *minio.Client // MinIO 客户端（兼容 AWS S3 API）
	bucket string        // 存储桶名称
}

// prefix 返回题目测试数据在 S3 中的前缀（类似目录路径）。
func (b *S3Backend) prefix(problemID uint) string {
	return fmt.Sprintf("testcases/%d/", problemID)
}

// SaveTestCases 保存测试用例到 S3。
// 先删除该题目下的所有现有对象，再上传新文件。
func (b *S3Backend) SaveTestCases(problemID uint, files map[string][]byte) error {
	ctx := context.Background()
	prefix := b.prefix(problemID)

	// 删除该题目下的旧文件
	objCh := b.client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	for obj := range objCh {
		b.client.RemoveObject(ctx, b.bucket, obj.Key, minio.RemoveObjectOptions{})
	}

	// 上传新文件
	for name, content := range files {
		key := prefix + name
		_, err := b.client.PutObject(ctx, b.bucket, key, bytes.NewReader(content), int64(len(content)), minio.PutObjectOptions{})
		if err != nil {
			return fmt.Errorf("failed to upload %s: %w", key, err)
		}
	}
	return nil
}

// ReadTestCase 从 S3 读取单个测试用例文件。
func (b *S3Backend) ReadTestCase(problemID uint, filename string) ([]byte, error) {
	ctx := context.Background()
	key := b.prefix(problemID) + filename
	obj, err := b.client.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(obj)
}

// ListTestCases 列出 S3 中题目的所有测试用例文件。
// 通过 ListObjects 遍历前缀下的所有对象。
func (b *S3Backend) ListTestCases(problemID uint) ([]FileInfo, error) {
	ctx := context.Background()
	prefix := b.prefix(problemID)
	var infos []FileInfo
	for obj := range b.client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		name := strings.TrimPrefix(obj.Key, prefix)
		if name == "" {
			continue
		}
		infos = append(infos, FileInfo{Name: name, Size: obj.Size})
	}
	return infos, nil
}

// DeleteTestCases 删除 S3 中题目的所有测试用例文件。
func (b *S3Backend) DeleteTestCases(problemID uint) error {
	ctx := context.Background()
	prefix := b.prefix(problemID)
	for obj := range b.client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		b.client.RemoveObject(ctx, b.bucket, obj.Key, minio.RemoveObjectOptions{})
	}
	return nil
}
