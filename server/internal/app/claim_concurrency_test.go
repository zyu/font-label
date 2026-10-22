package app

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/db"
)

// TestConcurrentClaimNoDoubleAssignment is the single most important correctness
// property of the product: "领取之后的图片不能再次领取".
//
// It hammers the exact SQL used by POST /claims (claimPickSQL) from 50 goroutines
// at once and asserts that every image is handed out exactly once.
//
// Run with:
//
//	TEST_DATABASE_URL='postgres://...' go test ./internal/app -run ConcurrentClaim -v
func TestConcurrentClaimNoDoubleAssignment(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run the concurrency test")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// 关闭必须先登记（t.Cleanup 后进先出），否则数据清理会在连接池已关闭后执行、
	// 静默失败，留下测试文件夹（踩过一次）。
	t.Cleanup(database.Close)

	const (
		workers       = 50
		perWorker     = 5
		totalImages   = workers * perWorker
		testDayOffset = "2001-01-01" // a day that no real upload uses
	)
	suffix := time.Now().UnixNano()

	var folderID int64
	if err := database.Pool.QueryRow(ctx,
		`INSERT INTO folders (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("concurrency-%d", suffix)).Scan(&folderID); err != nil {
		t.Fatalf("insert folder: %v", err)
	}

	userIDs := make([]int64, 0, 5)
	for i := 0; i < 5; i++ {
		var id int64
		if err := database.Pool.QueryRow(ctx,
			`INSERT INTO users (username, password_hash, display_name, role)
			 VALUES ($1,'x','并发测试','annotator') RETURNING id`,
			fmt.Sprintf("cc-%d-%d", suffix, i)).Scan(&id); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		userIDs = append(userIDs, id)
	}

	for i := 0; i < totalImages; i++ {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO images (folder_id, stored_name, day, seq, orig_name, ext, mime, bytes,
			                     sha256, rel_path, status)
			 VALUES ($1,$2,$3::date,$4,'t.png','png','image/png',1,
			         lpad(to_hex($5::int), 64, '0'), $6, 'unassigned')`,
			folderID,
			fmt.Sprintf("cc-%d-%03d.png", suffix, i+1),
			testDayOffset, i+1, i+1,
			fmt.Sprintf("cc/%d.png", i+1)); err != nil {
			t.Fatalf("insert image %d: %v", i, err)
		}
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = database.Pool.Exec(cleanupCtx, `DELETE FROM claims WHERE folder_id=$1`, folderID)
		_, _ = database.Pool.Exec(cleanupCtx, `DELETE FROM images WHERE folder_id=$1`, folderID)
		_, _ = database.Pool.Exec(cleanupCtx, `DELETE FROM folders WHERE id=$1`, folderID)
		for _, id := range userIDs {
			_, _ = database.Pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, id)
		}
	})

	var (
		mu        sync.Mutex
		granted   []int64
		failures  []error
		wg        sync.WaitGroup
		startGate = make(chan struct{})
	)
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-startGate // fire everything at once
			userID := userIDs[worker%len(userIDs)]
			var got []int64
			err := database.Tx(ctx, func(tx pgx.Tx) error {
				rows, err := tx.Query(ctx, claimPickSQL, folderID, perWorker, userID, 24)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var c claimedImage
					if err := rows.Scan(&c.ID, &c.FolderID, &c.StoredName, &c.Seq); err != nil {
						return err
					}
					got = append(got, c.ID)
				}
				return rows.Err()
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, err)
				return
			}
			granted = append(granted, got...)
		}(w)
	}
	close(startGate)
	wg.Wait()
	elapsed := time.Since(start)

	if len(failures) > 0 {
		t.Fatalf("%d goroutine(s) failed, first: %v", len(failures), failures[0])
	}
	seen := make(map[int64]int, len(granted))
	for _, id := range granted {
		seen[id]++
	}
	var dupes []int64
	for id, n := range seen {
		if n > 1 {
			dupes = append(dupes, id)
		}
	}
	if len(dupes) > 0 {
		t.Fatalf("同一张图被发放了 %d 次（示例 id: %v）", len(dupes), dupes[:min(len(dupes), 5)])
	}
	if len(granted) != totalImages {
		t.Fatalf("发放总数 = %d, want %d", len(granted), totalImages)
	}

	// Independent check against the database: everyone who got an image is the
	// recorded owner, and nothing is left in the pool.
	var assigned, unassigned int
	if err := database.Pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status='assigned'), count(*) FILTER (WHERE status='unassigned')
		   FROM images WHERE folder_id=$1`, folderID).Scan(&assigned, &unassigned); err != nil {
		t.Fatalf("count: %v", err)
	}
	if assigned != totalImages || unassigned != 0 {
		t.Fatalf("库里状态不对: assigned=%d unassigned=%d", assigned, unassigned)
	}
	t.Logf("%d 张 / %d 并发，耗时 %v，无重复无遗漏", totalImages, workers, elapsed.Round(time.Millisecond))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
