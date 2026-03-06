package batchresolver

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

import "sync/atomic"

type Resolver struct {
	users                   []*User
	profiles                []*Profile
	images                  []*Image
	profileErrIdx           int
	profileErrWithValueIdxs map[int]struct{}
	profileErrListIdxs      map[int]struct{}
	profileGqlErrNoPathIdxs map[int]struct{}
	profileGqlErrPathIdxs   map[int]struct{}
	profileWrongLen         bool
	batchErrsWrongLen       bool
	batchErrsLen            int
	batchResultsWrongLen    bool
	batchResultsLen         int
	batchErrListIdxs        map[int]struct{}

	// childUserMap maps a parent *User to its child *User for childUserBatch.
	childUserMap map[*User]*User
	// allUserIndex maps any *User pointer (including child users across levels)
	// to a logical index for profile lookup. When set, userIndex falls back to
	// this after checking r.users.
	allUserIndex map[*User]int

	// Call counters for the nested batch performance test (atomic for -race safety)
	profileBatchCalls              atomic.Int32
	profileNonBatchCalls           atomic.Int32
	coverBatchCalls                atomic.Int32
	coverNonBatchCalls             atomic.Int32
	profileConnectionBatchCalls    atomic.Int32
	profileConnectionNonBatchCalls atomic.Int32
	childUserBatchCalls            atomic.Int32
	childUserNonBatchCalls         atomic.Int32
}

func (r *Resolver) userIndex(obj *User) int {
	if obj == nil {
		return -1
	}
	for i := range r.users {
		if r.users[i] == obj {
			return i
		}
	}
	if r.allUserIndex != nil {
		if idx, ok := r.allUserIndex[obj]; ok {
			return idx
		}
	}
	return -1
}

func (r *Resolver) profileIndex(obj *Profile) int {
	if obj == nil {
		return -1
	}
	for i := range r.profiles {
		if r.profiles[i] == obj {
			return i
		}
	}
	return -1
}
