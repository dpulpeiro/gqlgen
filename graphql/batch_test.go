package graphql

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestBatchErrorList_UnwrapFiltersNil(t *testing.T) {
	sentinel := errors.New("sentinel")
	list := BatchErrorList{nil, sentinel, nil}

	type unwrapper interface {
		Unwrap() []error
	}
	u, ok := any(list).(unwrapper)
	require.True(t, ok)

	got := u.Unwrap()
	require.Len(t, got, 1)
	require.Equal(t, sentinel, got[0])
}

func TestBatchErrorList_ErrorsIs(t *testing.T) {
	sentinel := errors.New("sentinel")
	other := errors.New("other")
	list := BatchErrorList{nil, sentinel, other}

	require.ErrorIs(t, list, sentinel)
	require.ErrorIs(t, list, other)
	require.NotErrorIs(t, list, errors.New("missing"))
}

func TestBatchErrorList_ErrorsIsWithAllNil(t *testing.T) {
	list := BatchErrorList{nil, nil}

	require.NotErrorIs(t, list, errors.New("missing"))
}

func newBatchTestContext() context.Context {
	ctx := WithResponseContext(context.Background(), DefaultErrorPresenter, nil)
	ctx = WithPathContext(ctx, NewPathWithField("users"))
	ctx = WithPathContext(ctx, NewPathWithIndex(0))
	ctx = WithPathContext(ctx, NewPathWithField("profile"))
	return ctx
}

func TestResolveBatchGroupResult_Success(t *testing.T) {
	ctx := newBatchTestContext()
	result := &BatchFieldResult{
		Results: []string{"a", "b"},
	}

	got, err := ResolveBatchGroupResult[string](
		ctx,
		ast.PathIndex(1),
		2,
		result,
		"User.profile",
	)
	require.NoError(t, err)
	require.Equal(t, "b", got)
	require.Empty(t, GetErrors(ctx))
}

func TestResolveBatchGroupResult_ResultLenMismatch(t *testing.T) {
	ctx := newBatchTestContext()
	result := &BatchFieldResult{
		Results: []string{"a"},
	}

	got, err := ResolveBatchGroupResult[string](
		ctx,
		ast.PathIndex(1),
		2,
		result,
		"User.profile",
	)
	require.NoError(t, err)
	require.Nil(t, got)

	errs := GetErrors(ctx)
	require.Len(t, errs, 1)
	require.Equal(
		t,
		"index 1: batch resolver User.profile returned 1 results for 2 "+
			"parents",
		errs[0].Message,
	)
	require.Equal(
		t,
		ast.Path{
			ast.PathName("users"),
			ast.PathIndex(1),
			ast.PathName("profile"),
		},
		errs[0].Path,
	)
}

func TestResolveBatchSingleResult_BatchErrors(t *testing.T) {
	ctx := newBatchTestContext()

	got, err := ResolveBatchSingleResult[string](
		ctx,
		[]string{"a"},
		BatchErrorList{errors.New("boom")},
		"User.profile",
	)
	require.NoError(t, err)
	require.Nil(t, got)

	errs := GetErrors(ctx)
	require.Len(t, errs, 1)
	require.Equal(t, "boom", errs[0].Message)
}

func TestResolveBatchSingleResult_ErrorLenMismatch(t *testing.T) {
	ctx := newBatchTestContext()

	got, err := ResolveBatchSingleResult[string](
		ctx,
		[]string{"a"},
		BatchErrorList{},
		"User.profile",
	)
	require.NoError(t, err)
	require.Nil(t, got)

	errs := GetErrors(ctx)
	require.Len(t, errs, 1)
	require.Equal(
		t,
		"batch resolver User.profile returned 0 errors for 1 "+
			"parents (index 0)",
		errs[0].Message,
	)
}

func TestNewBatchParentGroup_BuildsIndexMap(t *testing.T) {
	type Profile struct{ ID string }
	p1 := &Profile{ID: "p1"}
	p2 := &Profile{ID: "p2"}
	p3 := &Profile{ID: "p3"}

	group := NewBatchParentGroup([]*Profile{p1, p2, p3})

	require.Equal(t, []*Profile{p1, p2, p3}, group.Parents)

	idx, ok := group.IndexOf(p1)
	require.True(t, ok)
	require.Equal(t, 0, idx)

	idx, ok = group.IndexOf(p2)
	require.True(t, ok)
	require.Equal(t, 1, idx)

	idx, ok = group.IndexOf(p3)
	require.True(t, ok)
	require.Equal(t, 2, idx)
}

func TestNewBatchParentGroup_IndexOf_NotFound(t *testing.T) {
	type Profile struct{ ID string }
	p1 := &Profile{ID: "p1"}
	unknown := &Profile{ID: "unknown"}

	group := NewBatchParentGroup([]*Profile{p1})

	_, ok := group.IndexOf(unknown)
	require.False(t, ok)
}

func TestBatchParentGroup_IndexOf_NilGroup(t *testing.T) {
	var group *BatchParentGroup
	_, ok := group.IndexOf("anything")
	require.False(t, ok)
}

func TestBatchParentGroup_IndexOf_NoIndexMap(t *testing.T) {
	group := &BatchParentGroup{Parents: []string{"a", "b"}}
	_, ok := group.IndexOf("a")
	require.False(t, ok)
}

func TestGetFieldResult_RecoversPanic(t *testing.T) {
	group := &BatchParentGroup{Parents: []string{"a", "b", "c"}}

	const n = 10
	results := make([]*BatchFieldResult, n)
	var wg sync.WaitGroup

	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = group.GetFieldResult("panicking", func() (any, error) {
				panic("resolver blew up")
			})
		}()
	}
	wg.Wait()

	for i := range n {
		require.NotNil(t, results[i], "goroutine %d should get a result", i)
		require.Nil(t, results[i].Results, "Results should be nil after panic")
		require.Error(t, results[i].Err)
		require.Contains(t, results[i].Err.Error(), "panic in batch resolver")
		require.Contains(t, results[i].Err.Error(), "resolver blew up")
	}
}

func TestNewBatchParentGroup_DeduplicatesDuplicatePointers(t *testing.T) {
	type Profile struct{ ID string }
	p1 := &Profile{ID: "p1"}
	p2 := &Profile{ID: "p2"}

	// p1 appears at positions 0 and 2 in the input (e.g. from a dataloader cache).
	// The deduplicated Parents should be [p1, p2] and IndexOf should return
	// indices into that deduplicated slice.
	group := NewBatchParentGroup([]*Profile{p1, p2, p1})

	parents := group.Parents.([]*Profile)
	require.Len(t, parents, 2, "duplicates should be removed")
	require.Same(t, p1, parents[0])
	require.Same(t, p2, parents[1])

	idx, ok := group.IndexOf(p1)
	require.True(t, ok)
	require.Equal(t, 0, idx)

	idx, ok = group.IndexOf(p2)
	require.True(t, ok)
	require.Equal(t, 1, idx)
}

func TestGetFieldResult_DeduplicatesAcrossGoroutines(t *testing.T) {
	group := &BatchParentGroup{Parents: []string{"a", "b", "c"}}

	const n = 20
	var callCount atomic.Int32
	results := make([]*BatchFieldResult, n)
	var wg sync.WaitGroup

	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = group.GetFieldResult("cover", func() (any, error) {
				callCount.Add(1)
				return []string{"img-a", "img-b", "img-c"}, nil
			})
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), callCount.Load(), "resolve should execute exactly once")
	for i := 1; i < n; i++ {
		require.Same(t, results[0], results[i], "all goroutines should get the same result")
	}
	require.Equal(t, []string{"img-a", "img-b", "img-c"}, results[0].Results)
}
