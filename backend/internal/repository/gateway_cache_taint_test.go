package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 两个 gatewayCache 实例共享同一 Redis，模拟多副本部署——任一副本记录的
// 切号事实，其它副本必须可见（这是溯源表迁 Redis 的全部意义）。
func TestCodexSessionTaint_SharedAcrossReplicas(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	replica1 := NewGatewayCache(rdb)
	replica2 := NewGatewayCache(rdb)
	ctx := context.Background()
	const seed = "7\x00sess-shared"

	// 副本 1：会话由账号 1 服务若干轮，从未切换。
	for i := 0; i < 3; i++ {
		switched, err := replica1.NoteCodexSessionTaint(ctx, seed, 1, time.Hour)
		require.NoError(t, err)
		require.False(t, switched)
	}

	// 副本 2：切换到账号 2——副本 1 记录的首账号在这里可见，置位切换。
	switched, err := replica2.NoteCodexSessionTaint(ctx, seed, 2, time.Hour)
	require.NoError(t, err)
	require.True(t, switched)

	// 副本 1 的后续请求立即看到切换（每轮都净化）。
	switched, err = replica1.NoteCodexSessionTaint(ctx, seed, 1, time.Hour)
	require.NoError(t, err)
	require.True(t, switched)
	ok, err := replica1.IsCodexSessionTainted(ctx, seed)
	require.NoError(t, err)
	require.True(t, ok)

	// 切换标志单向：回到首账号也不复位。
	switched, err = replica2.NoteCodexSessionTaint(ctx, seed, 1, time.Hour)
	require.NoError(t, err)
	require.True(t, switched)
}

// TTL 过期后溯源遗忘，重新积累（与原内存表语义一致）。
func TestCodexSessionTaint_TTLExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := NewGatewayCache(rdb)
	ctx := context.Background()

	switched, err := cache.NoteCodexSessionTaint(ctx, "1\x00sess-ttl", 1, time.Second)
	require.NoError(t, err)
	require.False(t, switched)

	mr.FastForward(2 * time.Second)

	ok, err := cache.IsCodexSessionTainted(ctx, "1\x00sess-ttl")
	require.NoError(t, err)
	require.False(t, ok)
	// 过期后重新记录：新首账号，未切换。
	switched, err = cache.NoteCodexSessionTaint(ctx, "1\x00sess-ttl", 2, time.Second)
	require.NoError(t, err)
	require.False(t, switched)
}

// 未记录的会话查询返回 (false, nil)，空 seed / 非法账号为无操作。
func TestCodexSessionTaint_EdgeInputs(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := NewGatewayCache(rdb)
	ctx := context.Background()

	ok, err := cache.IsCodexSessionTainted(ctx, "9\x00never-recorded")
	require.NoError(t, err)
	require.False(t, ok)

	switched, err := cache.NoteCodexSessionTaint(ctx, "", 5, time.Hour)
	require.NoError(t, err)
	require.False(t, switched)
	switched, err = cache.NoteCodexSessionTaint(ctx, "9\x00x", 0, time.Hour)
	require.NoError(t, err)
	require.False(t, switched)

	_ = switched
}
