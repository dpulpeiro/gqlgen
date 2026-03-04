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
		"",
		nil,
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
		"",
		nil,
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

func TestBatchFieldResult_GetChildGroup_ReturnsSameInstance(t *testing.T) {
	result := &BatchFieldResult{
		done:    make(chan struct{}),
		Results: []string{"a", "b", "c"},
	}

	g1 := result.GetChildGroup()
	g2 := result.GetChildGroup()

	require.Same(t, g1, g2, "GetChildGroup must return the same pointer on subsequent calls")
	require.Equal(t, []string{"a", "b", "c"}, g1.Parents, "Parents should be set from Results")
}

func TestBatchFieldResult_GetChildGroup_SharedAcrossGoroutines(t *testing.T) {
	result := &BatchFieldResult{
		done:    make(chan struct{}),
		Results: []string{"a", "b", "c"},
	}

	const n = 10
	groups := make([]*BatchParentGroup, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			groups[i] = result.GetChildGroup()
		}()
	}
	wg.Wait()

	for i := 1; i < n; i++ {
		require.Same(t, groups[0], groups[i], "all goroutines must get the same group")
	}
}

func TestBatchParentIndex_FallsBackToBatchResultIndex(t *testing.T) {
	t.Run("from path", func(t *testing.T) {
		ctx := WithPathContext(context.Background(), NewPathWithField("users"))
		ctx = WithPathContext(ctx, NewPathWithIndex(3))
		ctx = WithPathContext(ctx, NewPathWithField("profileBatch"))

		idx, ok := BatchParentIndex(ctx)
		require.True(t, ok)
		require.Equal(t, ast.PathIndex(3), idx)
	})

	t.Run("from context", func(t *testing.T) {
		ctx := WithPathContext(context.Background(), NewPathWithField("users"))
		ctx = WithPathContext(ctx, NewPathWithIndex(0))
		ctx = WithPathContext(ctx, NewPathWithField("profileBatch"))
		ctx = WithPathContext(ctx, NewPathWithField("coverBatch"))
		ctx = withBatchResultIndex(ctx, 7)

		idx, ok := BatchParentIndex(ctx)
		require.True(t, ok)
		require.Equal(t, ast.PathIndex(7), idx)
	})

	t.Run("no index available", func(t *testing.T) {
		ctx := WithPathContext(context.Background(), NewPathWithField("users"))
		ctx = WithPathContext(ctx, NewPathWithField("profileBatch"))

		_, ok := BatchParentIndex(ctx)
		require.False(t, ok)
	})
}

func TestWithBatchParentGroup_SharesGroupAcrossContexts(t *testing.T) {
	group := &BatchParentGroup{Parents: []string{"a", "b"}}
	ctx1 := withBatchParentGroup(context.Background(), "Profile", group)
	ctx2 := withBatchParentGroup(context.Background(), "Profile", group)

	g1 := GetBatchParentGroup(ctx1, "Profile")
	g2 := GetBatchParentGroup(ctx2, "Profile")

	require.Same(t, group, g1)
	require.Same(t, group, g2)
	require.Same(t, g1, g2, "both contexts must reference the same group instance")
}

func TestWithBatchParents_CreatesNewGroup(t *testing.T) {
	parents1 := []string{"a", "b"}
	parents2 := []string{"c", "d"}
	ctx1 := WithBatchParents(context.Background(), "User", parents1)
	ctx2 := WithBatchParents(context.Background(), "User", parents2)

	g1 := GetBatchParentGroup(ctx1, "User")
	g2 := GetBatchParentGroup(ctx2, "User")

	require.NotSame(t, g1, g2, "WithBatchParents creates independent groups")
	require.Equal(t, parents1, g1.Parents)
	require.Equal(t, parents2, g2.Parents)
}

func TestWithBatchParents_ClearsStaleBatchResultIndex(t *testing.T) {
	// Simulate nested batch propagation setting a batchResultIndex,
	// then a list marshal calling WithBatchParents. The stale index
	// should be cleared so that IndexOf-based lookup is used instead.
	ctx := context.Background()
	ctx = withBatchResultIndex(ctx, 5)

	// Verify the index is set before clearing.
	idx, ok := BatchParentIndex(ctx)
	require.True(t, ok)
	require.Equal(t, ast.PathIndex(5), idx)

	// WithBatchParents should clear the stale index.
	ctx = WithBatchParents(ctx, "ProfileEdge", []string{"e1", "e2"})

	_, ok = BatchParentIndex(ctx)
	require.False(t, ok, "stale batchResultIndex should be cleared after WithBatchParents")
}

func TestWithBatchParentGroup_PreservesExistingGroups(t *testing.T) {
	ctx := WithBatchParents(context.Background(), "User", []string{"u1", "u2"})
	profileGroup := &BatchParentGroup{Parents: []string{"p1", "p2"}}
	ctx = withBatchParentGroup(ctx, "Profile", profileGroup)

	require.NotNil(t, GetBatchParentGroup(ctx, "User"), "User group should still be present")
	require.Same(t, profileGroup, GetBatchParentGroup(ctx, "Profile"))
}

func TestWrapBatchChildContext_NoChildType(t *testing.T) {
	result := &BatchFieldResult{done: make(chan struct{}), Results: []string{"a"}}

	got := wrapBatchChildContext("a", result, "", 0, nil)
	require.Equal(t, "a", got, "should return unwrapped value when childTypeName is empty and no nested groups")
}

func TestWrapBatchChildContext_WithChildType(t *testing.T) {
	result := &BatchFieldResult{
		done:    make(chan struct{}),
		Results: []string{"a", "b", "c"},
	}

	got := wrapBatchChildContext("b", result, "Profile", 1, nil)

	bcc, ok := got.(*BatchChildContext)
	require.True(t, ok, "should return a *BatchChildContext")
	require.Equal(t, "b", bcc.Result)
	require.Equal(t, "Profile", bcc.ChildType)
	require.Equal(t, 1, bcc.ChildIndex)
	require.NotNil(t, bcc.ChildGroup, "should have a shared group")
	require.Equal(t, []string{"a", "b", "c"}, bcc.ChildGroup.Parents)
}

func TestWrapBatchChildContext_SharedGroup(t *testing.T) {
	result := &BatchFieldResult{
		done:    make(chan struct{}),
		Results: []string{"a", "b", "c"},
	}

	bcc0 := wrapBatchChildContext("a", result, "Profile", 0, nil).(*BatchChildContext)
	bcc1 := wrapBatchChildContext("b", result, "Profile", 1, nil).(*BatchChildContext)
	bcc2 := wrapBatchChildContext("c", result, "Profile", 2, nil).(*BatchChildContext)

	require.Same(t, bcc0.ChildGroup, bcc1.ChildGroup)
	require.Same(t, bcc1.ChildGroup, bcc2.ChildGroup)

	require.Equal(t, "a", bcc0.Result)
	require.Equal(t, "b", bcc1.Result)
	require.Equal(t, "c", bcc2.Result)
	require.Equal(t, 0, bcc0.ChildIndex)
	require.Equal(t, 1, bcc1.ChildIndex)
	require.Equal(t, 2, bcc2.ChildIndex)
}

func TestWrapBatchChildContext_WithNestedGroups(t *testing.T) {
	result := &BatchFieldResult{
		done:    make(chan struct{}),
		Results: []string{"a", "b"},
	}

	nestedGroups := map[string]*BatchParentGroup{
		"Profile": NewBatchParentGroup([]string{"p1", "p2"}),
	}

	got := wrapBatchChildContext("a", result, "ProfilesConnection", 0, nestedGroups)

	bcc, ok := got.(*BatchChildContext)
	require.True(t, ok, "should return a *BatchChildContext")
	require.Equal(t, "a", bcc.Result)
	require.NotNil(t, bcc.NestedGroups)
	require.Contains(t, bcc.NestedGroups, "Profile")
	require.Equal(t, []string{"p1", "p2"}, bcc.NestedGroups["Profile"].Parents)
}

func TestResolveBatchGroupResult_WithChildTypeName(t *testing.T) {
	ctx := newBatchTestContext()
	result := &BatchFieldResult{
		done:    make(chan struct{}),
		Results: []string{"a", "b"},
	}

	got, err := ResolveBatchGroupResult[string](
		ctx,
		ast.PathIndex(1),
		2,
		result,
		"User.profile",
		"Profile",
		nil,
	)
	require.NoError(t, err)

	bcc, ok := got.(*BatchChildContext)
	require.True(t, ok, "result should be wrapped in BatchChildContext")
	require.Equal(t, "b", bcc.Result)
	require.Equal(t, "Profile", bcc.ChildType)
	require.Equal(t, 1, bcc.ChildIndex)
	require.NotNil(t, bcc.ChildGroup)
}

func TestResolveBatchGroupResult_WithoutChildTypeName(t *testing.T) {
	ctx := newBatchTestContext()
	result := &BatchFieldResult{
		done:    make(chan struct{}),
		Results: []string{"a", "b"},
	}

	got, err := ResolveBatchGroupResult[string](
		ctx,
		ast.PathIndex(0),
		2,
		result,
		"User.name",
		"",
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "a", got, "should return raw value without wrapping")
}

func TestResolveBatchGroupResult_WithNestedGroups(t *testing.T) {
	ctx := newBatchTestContext()
	result := &BatchFieldResult{
		done:    make(chan struct{}),
		Results: []string{"a", "b"},
	}

	nestedGroups := map[string]*BatchParentGroup{
		"Profile": NewBatchParentGroup([]string{"p1", "p2"}),
	}

	got, err := ResolveBatchGroupResult[string](
		ctx,
		ast.PathIndex(0),
		2,
		result,
		"User.profileConnectionBatch",
		"ProfilesConnection",
		nestedGroups,
	)
	require.NoError(t, err)

	bcc, ok := got.(*BatchChildContext)
	require.True(t, ok, "result should be wrapped in BatchChildContext")
	require.Equal(t, "a", bcc.Result)
	require.Equal(t, "ProfilesConnection", bcc.ChildType)
	require.NotNil(t, bcc.NestedGroups)
	require.Contains(t, bcc.NestedGroups, "Profile")
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

func TestGetNestedGroups_ComputesOnce(t *testing.T) {
	result := &BatchFieldResult{
		done:    make(chan struct{}),
		Results: []string{"conn0", "conn1"},
	}

	var callCount atomic.Int32
	compute := func() map[string]*BatchParentGroup {
		callCount.Add(1)
		return map[string]*BatchParentGroup{
			"Profile": NewBatchParentGroup([]string{"p0", "p1"}),
		}
	}

	const n = 10
	allGroups := make([]map[string]*BatchParentGroup, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allGroups[i] = result.GetNestedGroups(compute)
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), callCount.Load(), "compute should execute exactly once")
	for i := 1; i < n; i++ {
		require.Same(t, allGroups[0]["Profile"], allGroups[i]["Profile"],
			"all goroutines should share the same Profile group")
	}
}
