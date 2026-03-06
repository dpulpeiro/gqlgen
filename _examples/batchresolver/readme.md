# Batch Resolver Example

This example demonstrates **batch field resolvers** in gqlgen — resolvers that receive a slice of parent objects and return results for all of them in a single call, instead of being invoked once per parent. It also covers **nested batch propagation**, which ensures that descendant batch resolvers also batch when their parents come from a batch resolver's results.

## Schema

```graphql
type Query {
  users: [User!]!
}

type User {
  # Basic batch/non-batch pairs for testing parity and errors
  nullableBatch: Profile
  nullableNonBatch: Profile
  nullableBatchWithArg(offset: Int!): Profile
  nullableNonBatchWithArg(offset: Int!): Profile
  nonNullableBatch: Profile!
  nonNullableNonBatch: Profile!

  # Nested batch propagation
  profileBatch: Profile              # direct nesting
  profileNonBatch: Profile
  profileConnectionBatch: ProfilesConnection  # connection path
  profileConnectionNonBatch: ProfilesConnection
  childUserBatch: User               # recursive self-referencing
  childUserNonBatch: User
}

type Profile {
  id: ID!
  coverBatch: Image
  coverNonBatch: Image
}

type Image {
  url: String!
}

type ProfilesConnection {
  edges: [ProfileEdge!]!
  totalCount: Int!
}

type ProfileEdge {
  node: Profile!
  cursor: ID!
}
```

## Configuration

In `gqlgen.yml`, batch resolvers are enabled per-field:

```yaml
models:
  User:
    fields:
      nullableBatch:
        resolver: true
        batch: true
```

This changes the generated resolver signature from the standard single-object form:

```go
NullableNonBatch(ctx context.Context, obj *User) (*Profile, error)
```

to a batch form that receives all parents at once:

```go
NullableBatch(ctx context.Context, objs []*User) ([]*Profile, error)
```

## Per-Item Errors

Batch resolvers can return per-item errors using `graphql.BatchErrorList`:

```go
results := make([]*Profile, len(objs))
errs := make([]error, len(objs))
for i, obj := range objs {
    results[i], errs[i] = resolve(obj)
}
return results, graphql.BatchErrorList(errs)
```

Each entry in the error slice corresponds to the parent at the same index. Individual errors can also be `gqlerror.List` to report multiple errors for a single item. The framework validates that both the results and errors slices match the number of parents.

## Nested Batch Propagation

The main feature demonstrated here is how batch resolver results propagate batch context to descendant batch resolvers. Without this, each result from a batch resolver would be marshalled individually, causing child batch resolvers to be called N times instead of once.

### How it works

When gqlgen marshals a `[User]` list field, it sets a **batch parent context** on each element:

```
ctx = WithBatchParents(ctx, "User", []*User{u0, u1, ..., u9})
```

Each goroutine resolving a user inherits this context. The generated `resolveBatch_User_profileBatch` code retrieves the group via `GetBatchParentGroup(ctx, "User")`, and `GetFieldResult`'s `sync.Once` ensures the resolver is called exactly once — the first goroutine executes it, the rest wait and reuse the result.

The question is: what happens to the *results* of that batch call? Each profile is dispatched to its own goroutine for marshalling. Without propagation, `coverBatch` on each profile would be called independently (N times). The propagation mechanism ensures it's called once.

### Direct nesting (profileBatch -> coverBatch)

```graphql
query {
  users {                        # 10 users
    profileBatch {               # batch: 1 call for all 10 users -> [p0..p9]
      coverBatch { url }         # batch: 1 call for all 10 profiles (propagated)
    }
  }
}
```

After `profileBatch` returns `[p0..p9]`, each result is wrapped in a `BatchChildContext`:

```go
BatchChildContext{
    Result:     p_i,
    ChildType:  "Profile",
    ChildGroup: sharedGroup,   // same pointer for all 10 goroutines
    ChildIndex: i,
}
```

`resolveField` (in `graphql/resolve_field.go`) detects the wrapper, unpacks it, and puts the shared `BatchParentGroup` into context:

```go
ctx = withBatchParentGroup(ctx, bcc.ChildType, bcc.ChildGroup)
ctx = withBatchResultIndex(ctx, bcc.ChildIndex)
```

Now all 10 goroutines share the same "Profile" group. When `resolveBatch_Profile_coverBatch` runs, `GetFieldResult`'s `sync.Once` ensures one goroutine calls the resolver while the others wait.

**Key components:**

| Component | Role |
|-----------|------|
| `GetChildGroup()` | Creates a shared `BatchParentGroup` from batch results (via `sync.Once`) |
| `wrapBatchChildContext()` | Wraps each result with shared group + index |
| `resolveField` | Unwraps `BatchChildContext`, installs group into context |
| `BatchParentIndex()` | Returns the goroutine's index (from path or `batchResultIndex`) |
| `GetFieldResult()` | Deduplicates: `sync.Once` ensures resolver runs once per field alias |

### Connection path (profileConnectionBatch -> edges -> node -> coverBatch)

```graphql
query {
  users {                                # 10 users
    profileConnectionBatch {             # batch: 1 call -> []*ProfilesConnection
      edges {                            # non-batched intermediate
        node {                           # non-batched intermediate
          coverBatch { url }             # batch: should be 1 call
        }
      }
    }
  }
}
```

Direct child group propagation alone isn't enough here. The `BatchChildContext` carries a "ProfilesConnection" group, but `coverBatch` needs a "Profile" group. The profiles are buried inside `connection.Edges[].Node`.

**Solution: nested extraction.** At the batch call site, the generated code extracts nested objects from the results:

```go
// Generated in resolveBatch_User_profileConnectionBatch
nestedGroups := result.GetNestedGroups(func() map[string]*BatchParentGroup {
    results, ok := result.Results.([]*ProfilesConnection)
    if !ok { return nil }
    var extracted []*Profile
    for _, conn := range results {
        if conn == nil { continue }
        for _, edge := range conn.Edges {
            if edge == nil { continue }
            extracted = append(extracted, edge.Node)
        }
    }
    if len(extracted) > 0 {
        return map[string]*BatchParentGroup{
            "Profile": NewBatchParentGroup(extracted),
        }
    }
    return nil
})
```

`NewBatchParentGroup` builds an `indexMap` (pointer -> position) so that when a goroutine resolving a specific Profile calls `group.IndexOf(obj)`, it finds its position by pointer identity — no path-based index needed.

These nested groups are passed through `BatchChildContext.NestedGroups` and installed into context by `resolveField`:

```go
for typeName, group := range bcc.NestedGroups {
    ctx = withBatchParentGroup(ctx, typeName, group)
}
```

The extraction paths are computed at code generation time by `NestedBatchPaths()` in `codegen/field.go`, which traverses the schema from the return type down to descendant types that have batch fields.

### Recursive types (User -> childUserBatch -> User -> ...)

```graphql
query {
  users {                              # 10 root users
    childUserBatch {                   # batch: 1 call -> 10 L1 users
      childUserBatch {                 # batch: 1 call -> 10 L2 users
        childUserBatch {               # batch: 1 call -> 10 L3 users
          profileBatch { id }          # batch: 1 call for all L3 users
        }
      }
    }
  }
}
```

This works through direct nesting: each `childUserBatch` returns `[]*User`, which creates a "User" `BatchChildContext` with a shared group. The next level's `resolveBatch_User_childUserBatch` finds the group and batches. The chain continues for as many levels as needed.

### Identity check (stale group prevention)

When batch and non-batch resolvers are interleaved, a stale batch group from a previous level can survive in context. For example:

```graphql
query {
  users {
    childUserBatch {           # batch: creates "User" group with L1 users
      childUserNonBatch {      # non-batch: does NOT create a new group
        childUserBatch {       # batch: finds stale "User" group (L1 users!)
          profileBatch { id }
        }
      }
    }
  }
}
```

At level 3, the "User" group from level 1 is still in context (non-batch resolvers don't replace it). But `parents[idx]` would be an L1 user, while `obj` is an L2 user. The generated code includes an identity check:

```go
if ok && int(idx) >= 0 && int(idx) < len(parents) && parents[int(idx)] == obj {
    // Group is valid, use batch path
} else {
    // Group is stale, fall back to single-parent call
}
```

This `parents[int(idx)] == obj` check prevents using stale groups. If the check fails, the resolver falls through to `ResolveBatchSingleResult` (one call per parent).

### Mixed batch/non-batch behavior

A non-batch resolver in the chain breaks batch propagation for all downstream levels:

| Pattern | childUserBatch calls | childUserNonBatch calls | profileBatch calls |
|---------|---------------------|------------------------|--------------------|
| `batch -> batch -> batch -> profileBatch` | 3 | 0 | 1 |
| `batch -> nonBatch -> batch -> profileBatch` | 1 + N | N | N |
| `nonBatch -> batch -> batch -> profileBatch` | N + N | N | N |
| `batch -> batch -> nonBatch -> profileNonBatch` | 2 | N | N |

Once the chain is broken, downstream batch resolvers fall back to per-parent calls because `ResolveBatchSingleResult` doesn't create a `BatchChildContext`.

### Aliased roots and fields

Each field alias creates an independent batch scope:

```graphql
query {
  usersA: users {
    connA: profileConnectionBatch {
      edges { node { coverBatch { url } } }
    }
    connB: profileConnectionBatch {
      edges { node { coverBatch { url } } }
    }
  }
  usersB: users {
    connC: profileConnectionBatch {
      edges { node { coverBatch { url } } }
    }
  }
}
```

| Resolver | Calls |
|----------|-------|
| `profileConnectionBatch` | 3 (one per alias: connA, connB, connC) |
| `coverBatch` | 3 (one per alias's extracted profiles) |

Profiles from `connA` and `connB` are never mixed — `GetFieldResult` keys by field alias.

## Tests

### Parity tests

Every batch field has a non-batch counterpart. Parity tests verify both produce identical data and errors:

- `TestBatchResolver_Parity_NoError` — successful resolution
- `TestBatchResolver_Parity_WithArgs` — arguments passed through
- `TestBatchResolver_Parity_Error` — error at specific index
- `TestBatchResolver_Parity_GqlErrorList` — multiple `gqlerror` per item
- `TestBatchResolver_Parity_GqlErrorPathNil` — gqlerror without path
- `TestBatchResolver_Parity_GqlErrorPathSet` — gqlerror with custom path
- `TestBatchResolver_Parity_PartialResponseWithErrValue` — error with value
- `TestBatchResolver_Parity_NonNullPropagation` — non-null field error nulls parent

### Validation tests

- `TestBatchResolver_InvalidLen_AddsErrorPerParent` — wrong result slice length
- `TestBatchResolver_BatchErrors_ErrLenMismatch_AddsErrorPerParent` — wrong error slice length
- `TestBatchResolver_BatchErrors_ResultLenMismatch_AddsErrorPerParent` — results/errors mismatch with BatchErrorList
- `TestBatchResolver_BatchErrors_ListPerIndex_AddsMultipleErrors` — multiple errors per item

### Nested batch tests

- `TestBatchResolver_Nested_CallCount` — direct nesting call counts + data parity
- `TestBatchResolver_Nested_Connection_CallCount` — connection path call counts + data parity
- `TestBatchResolver_Nested_ExpectedBatching` — comprehensive scenarios:
  - `SimpleNestedBatch` — 2 levels, 1 call each
  - `AliasedRoots` — independent scopes per root alias
  - `ConnectionNestedBatch` — propagation through intermediate types
  - `AliasedRootsAndConnections` — 4 independent connection scopes
- `TestBatchResolver_Nested_RecursiveUser` — 3 levels of `User -> User -> User -> Profile`
- `TestBatchResolver_Nested_RecursiveUser_Mixed` — interleaved batch/non-batch with identity check verification

### Benchmarks

`BenchmarkBatchResolver_SingleLevel` and `BenchmarkBatchResolver_Nested` compare batch vs non-batch execution time. These use in-memory resolvers with no I/O, so they only measure framework overhead. In practice, the main benefit of batching is reducing round-trips to external services.

## Architecture

### Code generation (`codegen/field.go` + `codegen/field.gotpl`)

For each batch field, the template generates a `resolveBatch_*` function that:

1. Looks up the `BatchParentGroup` for the parent type
2. Validates the group contains the current object (identity check)
3. Calls `GetFieldResult` to deduplicate the resolver call
4. For OBJECT return types, computes nested extraction paths via `NestedBatchPaths()`
5. Wraps results through `ResolveBatchGroupResult` -> `wrapBatchChildContext`
6. Falls back to `ResolveBatchSingleResult` when no valid group exists

### Runtime (`graphql/batch.go` + `graphql/resolve_field.go`)

| Type/Function | Purpose |
|---------------|---------|
| `BatchParentGroup` | Holds parent objects + `sync.Map` of field results + optional `indexMap` |
| `BatchFieldResult` | Cached resolver result with `sync.Once` deduplication |
| `BatchChildContext` | Wrapper carrying shared group, index, and nested groups |
| `NewBatchParentGroup[T]` | Creates group with `indexMap` for pointer-based `IndexOf` |
| `GetFieldResult` | Deduplicates via `sync.Once` keyed by field alias |
| `GetChildGroup` | Creates shared child group from batch results (via `sync.Once`) |
| `GetNestedGroups` | Extracts nested objects from batch results (via `sync.Once`) |
| `ResolveBatchGroupResult` | Picks result at index, validates lengths, wraps for propagation |
| `ResolveBatchSingleResult` | Fallback path for single-parent batch calls |
| `resolveField` | Unwraps `BatchChildContext`, installs groups into context |
