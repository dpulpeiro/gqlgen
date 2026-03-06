package graphql

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// BatchErrors represents per-item errors from a batch resolver.
// The returned slice must be the same length as the results slice, with nils for successes.
type BatchErrors interface {
	error
	Errors() []error
}

// BatchErrorList is a simple BatchErrors implementation backed by a slice.
type BatchErrorList []error

func (e BatchErrorList) Error() string   { return "batch resolver returned errors" }
func (e BatchErrorList) Errors() []error { return []error(e) }
func (e BatchErrorList) Unwrap() []error {
	if len(e) == 0 {
		return nil
	}
	out := make([]error, 0, len(e))
	for _, err := range e {
		if err != nil {
			out = append(out, err)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type batchContextKey struct{}

// BatchParentState holds the batch parent groups for the current context.
type BatchParentState struct {
	groups map[string]*BatchParentGroup
}

// BatchParentGroup represents a group of parent objects being resolved together.
type BatchParentGroup struct {
	Parents  any
	fields   sync.Map
	indexMap map[any]int // pointer → index for IndexOf
}

// NewBatchParentGroup creates a BatchParentGroup with an indexMap for pointer-based
// index lookup. This is used for nested batch propagation through intermediate types
// (e.g. connection → edges → node) where the path doesn't contain a PathIndex.
//
// Duplicate pointers are deduplicated: when the same pointer appears at multiple
// positions (common with dataloaders that return cached pointers), it is included
// only once in the Parents slice so the batch resolver is not called with
// redundant inputs and IndexOf always returns a unique index.
func NewBatchParentGroup[T any](parents []T) *BatchParentGroup {
	m := make(map[any]int, len(parents))
	deduped := make([]T, 0, len(parents))
	for _, p := range parents {
		if _, exists := m[any(p)]; !exists {
			m[any(p)] = len(deduped)
			deduped = append(deduped, p)
		}
	}
	return &BatchParentGroup{Parents: deduped, indexMap: m}
}

// IndexOf returns the index of obj in this group's parent slice.
// Uses pointer identity via the indexMap built at construction time.
func (g *BatchParentGroup) IndexOf(obj any) (int, bool) {
	if g == nil || g.indexMap == nil {
		return 0, false
	}
	idx, ok := g.indexMap[obj]
	return idx, ok
}

// BatchFieldResult represents the cached result of a batch field resolution.
type BatchFieldResult struct {
	once    sync.Once
	done    chan struct{}
	Results any
	Err     error
}

// WithBatchParents adds a batch parent group to the context.
func WithBatchParents(ctx context.Context, typeName string, parents any) context.Context {
	prev, _ := ctx.Value(batchContextKey{}).(*BatchParentState)
	var groups map[string]*BatchParentGroup
	if prev != nil {
		groups = make(map[string]*BatchParentGroup, len(prev.groups)+1)
		maps.Copy(groups, prev.groups)
	} else {
		groups = make(map[string]*BatchParentGroup, 1)
	}
	groups[typeName] = &BatchParentGroup{Parents: parents}

	return context.WithValue(ctx, batchContextKey{}, &BatchParentState{groups: groups})
}

// GetBatchParentGroup retrieves the batch parent group for a given type name from context.
func GetBatchParentGroup(ctx context.Context, typeName string) *BatchParentGroup {
	state, _ := ctx.Value(batchContextKey{}).(*BatchParentState)
	if state == nil {
		return nil
	}
	return state.groups[typeName]
}

// GetFieldResult retrieves or computes the result for a batch field.
func (g *BatchParentGroup) GetFieldResult(
	key string,
	resolve func() (any, error),
) *BatchFieldResult {
	if g == nil {
		return nil
	}
	res, _ := g.fields.LoadOrStore(key, &BatchFieldResult{done: make(chan struct{})})
	result := res.(*BatchFieldResult)
	result.once.Do(func() {
		defer close(result.done)
		defer func() {
			if r := recover(); r != nil {
				result.Err = fmt.Errorf("panic in batch resolver: %v", r)
			}
		}()
		result.Results, result.Err = resolve()
	})
	<-result.done
	return result
}

// BatchParentIndex returns the index of the current parent in the batch from the path.
func BatchParentIndex(ctx context.Context) (ast.PathIndex, bool) {
	path := GetPath(ctx)
	if len(path) < 2 {
		return 0, false
	}
	if idx, ok := path[len(path)-2].(ast.PathIndex); ok {
		return idx, true
	}
	return 0, false
}

// BatchPathWithIndex returns a copy of the current path with the parent index replaced.
func BatchPathWithIndex(ctx context.Context, index int) ast.Path {
	path := GetPath(ctx)
	if len(path) < 2 {
		return path
	}
	if _, ok := path[len(path)-2].(ast.PathIndex); !ok {
		return path
	}
	copied := make(ast.Path, len(path))
	copy(copied, path)
	copied[len(path)-2] = ast.PathIndex(index)
	return copied
}

// AddBatchError adds an error for a specific index in a batch operation.
func AddBatchError(ctx context.Context, index int, err error) {
	if err == nil {
		return
	}
	path := BatchPathWithIndex(ctx, index)
	if list, ok := err.(gqlerror.List); ok {
		for _, item := range list {
			if item == nil {
				continue
			}
			if item.Path == nil {
				cloned := *item
				cloned.Path = path
				AddError(ctx, &cloned)
				continue
			}
			AddError(ctx, item)
		}
		return
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		if gqlErr.Path == nil {
			cloned := *gqlErr
			cloned.Path = path
			AddError(ctx, &cloned)
			return
		}
		AddError(ctx, gqlErr)
		return
	}
	AddError(ctx, gqlerror.WrapPath(path, err))
}

// ResolveBatchGroupResult handles batch resolver results for grouped parents.
func ResolveBatchGroupResult[T any](
	ctx context.Context,
	idx ast.PathIndex,
	parentsLen int,
	result *BatchFieldResult,
	fieldName string,
) (any, error) {
	idxInt := int(idx)
	if result.Err != nil {
		if batchErrs, ok := result.Err.(BatchErrors); ok {
			results, ok := result.Results.([]T)
			if !ok {
				AddBatchError(ctx, idxInt, fmt.Errorf(
					"batch resolver %s returned unexpected result type (index %d)",
					fieldName,
					idx,
				))
				return nil, nil
			}
			errs := batchErrs.Errors()
			if len(results) != parentsLen {
				AddBatchError(ctx, idxInt, fmt.Errorf(
					"index %d: batch resolver %s returned %d results for %d parents",
					idx,
					fieldName,
					len(results),
					parentsLen,
				))
				return nil, nil
			}
			if len(errs) != parentsLen {
				AddBatchError(ctx, idxInt, fmt.Errorf(
					"index %d: batch resolver %s returned %d errors for %d parents",
					idx,
					fieldName,
					len(errs),
					parentsLen,
				))
				return nil, nil
			}
			if idxInt < 0 || idxInt >= len(results) {
				AddBatchError(ctx, idxInt, fmt.Errorf(
					"batch resolver %s could not resolve parent index %d",
					fieldName,
					idx,
				))
				return nil, nil
			}
			if err := errs[idxInt]; err != nil {
				AddBatchError(ctx, idxInt, err)
				return nil, nil
			}
			return results[idxInt], nil
		}
		AddBatchError(ctx, idxInt, result.Err)
		return nil, nil
	}

	results, ok := result.Results.([]T)
	if !ok {
		AddBatchError(ctx, idxInt, fmt.Errorf(
			"batch resolver %s returned unexpected result type (index %d)",
			fieldName,
			idx,
		))
		return nil, nil
	}
	if len(results) != parentsLen {
		AddBatchError(ctx, idxInt, fmt.Errorf(
			"index %d: batch resolver %s returned %d results for %d parents",
			idx,
			fieldName,
			len(results),
			parentsLen,
		))
		return nil, nil
	}
	if idxInt < 0 || idxInt >= len(results) {
		AddBatchError(ctx, idxInt, fmt.Errorf(
			"batch resolver %s could not resolve parent index %d",
			fieldName,
			idx,
		))
		return nil, nil
	}
	return results[idxInt], nil
}

// ResolveBatchSingleResult handles batch resolver results for a single parent.
func ResolveBatchSingleResult[T any](
	ctx context.Context,
	results []T,
	err error,
	fieldName string,
) (any, error) {
	if err != nil {
		if batchErrs, ok := err.(BatchErrors); ok {
			errs := batchErrs.Errors()
			if len(results) != 1 {
				AddBatchError(ctx, 0, fmt.Errorf(
					"batch resolver %s returned %d results for %d parents (index %d)",
					fieldName,
					len(results),
					1,
					0,
				))
				return nil, nil
			}
			if len(errs) != 1 {
				AddBatchError(ctx, 0, fmt.Errorf(
					"batch resolver %s returned %d errors for %d parents (index %d)",
					fieldName,
					len(errs),
					1,
					0,
				))
				return nil, nil
			}
			if errs[0] != nil {
				AddBatchError(ctx, 0, errs[0])
				return nil, nil
			}
			return results[0], nil
		}
		AddBatchError(ctx, 0, err)
		return nil, nil
	}
	if len(results) != 1 {
		AddBatchError(ctx, 0, fmt.Errorf(
			"batch resolver %s returned %d results for %d parents (index %d)",
			fieldName,
			len(results),
			1,
			0,
		))
		return nil, nil
	}
	return results[0], nil
}
