# Nested Batch Resolver Propagation Problem

## Summary

Batch resolvers in gqlgen do not propagate their batch context to child resolvers. When a batch resolver returns N results, each result is marshalled individually, so any batch-enabled child resolver is invoked N times (once per parent) instead of once for all parents.

## Example

Given the schema:

```graphql
type User {
  profileBatch: Profile    # batch resolver
  profileNonBatch: Profile # standard resolver
}

type Profile {
  coverBatch: Image    # batch resolver
  coverNonBatch: Image # standard resolver
}
```

And a query requesting the full chain:

```graphql
query {
  users {
    profile: profileBatch {
      id
      cover: coverBatch { url }
    }
  }
}
```

With 10 users:

| Resolver | Expected Calls | Actual Calls |
|---|---|---|
| `profileBatch` | 1 | 1 |
| `coverBatch` | 1 | **10** |

The first-level batch works correctly: `profileBatch` is called once with all 10 users. However, `coverBatch` is called 10 times (once per profile) instead of being batched into a single call with all 10 profiles.

The non-batch path calls both resolvers 10 times each, as expected. Both paths produce identical data.

## Root Cause

When gqlgen marshals a `[User]` list field, it sets a **batch parent context** on each element. Child batch resolvers collect all parents sharing the same context and resolve them in one call.

The problem is that results returned by a batch resolver are marshalled as **individual values**, not as a list. The batch parent context for `Profile` is only set when marshalling a `[Profile]` list field directly — it is not carried over from the batch resolver's returned slice. Each profile is dispatched independently, so `coverBatch` sees each profile as a standalone parent and is invoked once per profile.

```
[User] list field
  └─ sets batch parent context for User ✅
  └─ profileBatch(ctx, []*User) → []*Profile  (1 call) ✅
       └─ each *Profile marshalled individually
       └─ NO batch parent context set for Profile ❌
       └─ coverBatch(ctx, []*Profile{single}) called per profile (N calls) ❌
```

## Connection Variant

The same problem occurs through connection types:

```graphql
type User {
  profileConnectionBatch: ProfilesConnection # batch resolver
}

type ProfilesConnection {
  edges: [ProfileEdge!]!
}

type ProfileEdge {
  node: Profile!
}
```

Even though `profileConnectionBatch` resolves all connections in one call, the profiles nested inside `edges[].node` lack batch parent context. `coverBatch` on each `Profile` node is still called N times.

## Expected Behavior

Batch resolver results should propagate batch parent context to their children. Each batch scope is determined by the **field execution path** — separate root aliases and separate field aliases each get their own independent batch group.

```
[User] list field
  └─ sets batch parent context for User ✅
  └─ profileBatch(ctx, []*User) → []*Profile  (1 call) ✅
       └─ profiles treated as a batch group
       └─ batch parent context set for Profile ✅
       └─ coverBatch(ctx, []*Profile{all 10}) called once (1 call) ✅
```

### Schema

```graphql
type Query {
  users(first: Int): [User!]!
}

type User {
  profileBatch: Profile
  profileConnectionBatch: ProfilesConnection
}

type Profile {
  id: ID!
  coverBatch: Image
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

### Query 1: Simple nested batch

```graphql
query {
  users {
    profileBatch {
      id
      coverBatch { url }
    }
  }
}
```

| Resolver | Expected Calls | Batch Group |
|---|---|---|
| `users` | 1 | — |
| `profileBatch` | 1 | All users |
| `coverBatch` | 1 | All profiles from `profileBatch` |

### Query 2: Aliased roots

```graphql
query {
  usersA: users {
    profileBatch {
      id
      coverBatch { url }
    }
  }
  usersB: users {
    profileBatch {
      id
      coverBatch { url }
    }
  }
}
```

| Resolver | Expected Calls | Batch Group |
|---|---|---|
| `usersA: users` | 1 | — |
| `usersA → profileBatch` | 1 | All users in `usersA` |
| `usersA → coverBatch` | 1 | All profiles from `usersA` |
| `usersB: users` | 1 | — |
| `usersB → profileBatch` | 1 | All users in `usersB` |
| `usersB → coverBatch` | 1 | All profiles from `usersB` |

`usersA` and `usersB` are independent batch scopes. Their profiles are never mixed.

### Query 3: Connection nested batch

```graphql
query {
  users {
    profileConnectionBatch {
      edges {
        node {
          id
          coverBatch { url }
        }
      }
    }
  }
}
```

| Resolver | Expected Calls | Batch Group |
|---|---|---|
| `users` | 1 | — |
| `profileConnectionBatch` | 1 | All users |
| `coverBatch` | 1 | All profiles across all connection edges |

Batch context must propagate through non-batched intermediate types (`ProfilesConnection`, `ProfileEdge`) so that all `Profile` nodes from the batch-resolved connections are grouped together.

### Query 4: Aliased roots + aliased connection fields

```graphql
query {
  usersA: users {
    profileConnectionA: profileConnectionBatch {
      edges {
        node {
          id
          coverBatch { url }
        }
      }
    }
    profileConnectionB: profileConnectionBatch {
      edges {
        node {
          id
          coverBatch { url }
        }
      }
    }
  }
  usersB: users {
    profileConnectionC: profileConnectionBatch {
      edges {
        node {
          id
          coverBatch { url }
        }
      }
    }
    profileConnectionD: profileConnectionBatch {
      edges {
        node {
          id
          coverBatch { url }
        }
      }
    }
  }
}
```

| Resolver | Expected Calls | Batch Group |
|---|---|---|
| `usersA: users` | 1 | — |
| `usersA → profileConnectionA` | 1 | All users in `usersA` |
| `usersA → profileConnectionA → coverBatch` | 1 | All profiles in `profileConnectionA` edges |
| `usersA → profileConnectionB` | 1 | All users in `usersA` |
| `usersA → profileConnectionB → coverBatch` | 1 | All profiles in `profileConnectionB` edges |
| `usersB: users` | 1 | — |
| `usersB → profileConnectionC` | 1 | All users in `usersB` |
| `usersB → profileConnectionC → coverBatch` | 1 | All profiles in `profileConnectionC` edges |
| `usersB → profileConnectionD` | 1 | All users in `usersB` |
| `usersB → profileConnectionD → coverBatch` | 1 | All profiles in `profileConnectionD` edges |

Each aliased field creates its own batch scope. `profileConnectionA` and `profileConnectionB` are separate batch groups even though they resolve the same field on the same parent list. Batch context flows independently through each path.

## Test References

The current behavior is documented and verified in `_examples/batchresolver/batchresolver_test.go`:

- `TestBatchResolver_Nested_CallCount` — asserts `coverBatchCalls == N` (lines 514-519)
- `TestBatchResolver_Nested_Connection_CallCount` — asserts `coverBatchCalls == N` through connections (lines 627-635)

Both tests include TODO comments noting that `coverBatchCalls` should ideally be 1.
