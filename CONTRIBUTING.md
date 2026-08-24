# Contributing

## Build and test

```bash
go build -o netease-cli .
go test ./...
```

`netease_test.go` covers the mapping layer with recorded upstream
shapes. Every behavior change lands with a test in the same commit:
the test fails before the change and passes after.

## The cross-repo schema contract

This resolver and [`qq-cli`](https://github.com/BrandNewJimZhang/qq-cli)
publish the SAME schema field for field, so one caller can fan out to
both and merge the rows. Any change to the envelope, a `data` shape, an
`error_class` or an exit code must land in BOTH repositories and bump
`schema_version` — a half-landed schema change breaks every merged view
downstream.

## Commit messages

English, imperative mood, title ≤72 chars, prefixed with one of
`Feat:`, `Fix:`, `Refactor:`, `Docs:`, `Style:`, `Test:`, `Chore:`,
`Perf:`. The body records what changed and why.

## What will not be merged

- Anything that routes around a payment, DRM, region or entitlement
  refusal (see *No unlocking* in the README). `SkipUNM` stays pinned
  true.
- Audio downloading, caching or redistribution.
- New verbs or fields without a real consumer.
