package graphql

import (
	"context"
	"errors"
	"sync"
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

func TestGetFieldResult_DeduplicatesAcrossGoroutines(t *testing.T) {
	group := &BatchParentGroup{Parents: []string{"a", "b"}}

	var calls int32
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			group.GetFieldResult("name", func() (any, error) {
				mu.Lock()
				calls++
				mu.Unlock()
				return []string{"A", "B"}, nil
			})
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), calls)
}

func TestGetGroups_ComputedOnceAndShared(t *testing.T) {
	result := &BatchFieldResult{
		Results: []string{"x", "y"},
	}

	var calls int32
	var mu sync.Mutex
	compute := func() map[string]*BatchParentGroup {
		mu.Lock()
		calls++
		mu.Unlock()
		return map[string]*BatchParentGroup{
			"Profile": {Parents: []string{"x", "y"}},
			"Child":   {Parents: []string{"c1", "c2"}},
		}
	}

	var allGroups [10]map[string]*BatchParentGroup
	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		i := i
		go func() {
			defer wg.Done()
			allGroups[i] = result.GetGroups(compute)
		}()
	}
	wg.Wait()

	// Compute should be called exactly once.
	require.Equal(t, int32(1), calls)

	// All goroutines should get the same map instance.
	for i := 1; i < 10; i++ {
		require.Same(t, allGroups[0]["Profile"], allGroups[i]["Profile"])
		require.Same(t, allGroups[0]["Child"], allGroups[i]["Child"])
	}

	groups := result.GetGroups(compute)
	require.Equal(t, []string{"x", "y"}, groups["Profile"].Parents)
	require.Equal(t, []string{"c1", "c2"}, groups["Child"].Parents)
}

func TestBatchParentIndex_FindsLastPathIndex(t *testing.T) {
	// Direct list: users.3.name → finds 3
	ctx := WithResponseContext(context.Background(), DefaultErrorPresenter, nil)
	ctx = WithPathContext(ctx, NewPathWithField("users"))
	ctx = WithPathContext(ctx, NewPathWithIndex(3))
	ctx = WithPathContext(ctx, NewPathWithField("name"))

	idx, ok := BatchParentIndex(ctx)
	require.True(t, ok)
	require.Equal(t, ast.PathIndex(3), idx)
}

func TestBatchParentIndex_ThroughIntermediateFields(t *testing.T) {
	// Connection: edges.2.node.profile → finds 2
	ctx := WithResponseContext(context.Background(), DefaultErrorPresenter, nil)
	ctx = WithPathContext(ctx, NewPathWithField("edges"))
	ctx = WithPathContext(ctx, NewPathWithIndex(2))
	ctx = WithPathContext(ctx, NewPathWithField("node"))
	ctx = WithPathContext(ctx, NewPathWithField("profile"))

	idx, ok := BatchParentIndex(ctx)
	require.True(t, ok)
	require.Equal(t, ast.PathIndex(2), idx)
}

func TestBatchParentIndex_NoPathIndex(t *testing.T) {
	ctx := WithResponseContext(context.Background(), DefaultErrorPresenter, nil)
	ctx = WithPathContext(ctx, NewPathWithField("user"))
	ctx = WithPathContext(ctx, NewPathWithField("profile"))

	_, ok := BatchParentIndex(ctx)
	require.False(t, ok)
}

func TestWithBatchParentGroup_SharesGroupInstance(t *testing.T) {
	ctx := context.Background()
	group := &BatchParentGroup{Parents: []string{"a", "b"}}

	ctx = withBatchParentGroup(ctx, "User", group)

	got := GetBatchParentGroup(ctx, "User")
	require.Same(t, group, got)
}

func TestWithBatchParentGroup_PreservesPreviousGroups(t *testing.T) {
	ctx := context.Background()
	group1 := &BatchParentGroup{Parents: []string{"a"}}
	group2 := &BatchParentGroup{Parents: []string{"b"}}

	ctx = withBatchParentGroup(ctx, "User", group1)
	ctx = withBatchParentGroup(ctx, "Profile", group2)

	require.Same(t, group1, GetBatchParentGroup(ctx, "User"))
	require.Same(t, group2, GetBatchParentGroup(ctx, "Profile"))
}
