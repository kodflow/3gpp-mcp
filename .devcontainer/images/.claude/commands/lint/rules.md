# 148 Rules by Phase

## PHASE 1 - STRUCTURAL (fix FIRST - affects other phases)

```text
KTN-STRUCT-ONEFILE   → Split multi-struct files OR add dto:"..."
KTN-TEST-SUFFIX      → Rename _test.go → _external_test.go or _internal_test.go
KTN-TEST-INTPRIV     → Move private tests to _internal_test.go
KTN-TEST-EXTPUB      → Move public tests to _external_test.go
KTN-TEST-PKGNAME     → Fix test package name
KTN-CONST-ORDER      → Move const to top of file
KTN-VAR-ORDER        → Move var after const
```

## PHASE 2 - SIGNATURES (modify function signatures)

```text
KTN-FUNC-ERRLAST     → Put error as last return value
KTN-FUNC-CTXFIRST    → Put context.Context as first parameter
KTN-FUNC-MAXPARAM    → Group parameters or create struct
KTN-FUNC-NAMERET     → Add names to return values if >3
KTN-FUNC-GROUPARG    → Group params of same type
KTN-RECEIVER-MIXPTR  → Unify receiver (pointer or value)
KTN-RECEIVER-NAME    → Fix receiver name (1-2 chars)
```

## PHASE 3 - LOGIC (fix logic errors)

```text
KTN-VAR-SHADOW       → Rename shadowing variable
KTN-CONST-SHADOW     → Rename const that shadows builtin
KTN-FUNC-DEADCODE    → Remove unused function
KTN-FUNC-CYCLO       → Refactor overly complex function
KTN-FUNC-MAXSTMT     → Split function >35 statements
KTN-FUNC-MAXLOC      → Split function >50 LOC
KTN-VAR-TYPEASSERT   → Add ok check on type assertion
KTN-ERROR-WRAP       → Use %w in fmt.Errorf
KTN-ERROR-SENTINEL   → Create package-level sentinel error
KTN-GENERIC-*        → Fix generic constraints
KTN-ITER-*           → Fix iterator patterns
KTN-GOVET-*          → Fix all govet issues
```

## PHASE 4 - PERFORMANCE (memory optimizations)

```text
KTN-VAR-HOTLOOP      → Move allocation out of loop
KTN-VAR-BIGSTRUCT    → Pass by pointer if >64 bytes
KTN-VAR-SLICECAP     → Preallocate slice with capacity
KTN-VAR-MAPCAP       → Preallocate map with capacity
KTN-VAR-MAKEAPPEND   → Use make instead of append
KTN-VAR-GROW         → Use Buffer.Grow
KTN-VAR-STRBUILDER   → Use strings.Builder
KTN-VAR-STRCONV      → Avoid string() in loop
KTN-VAR-SYNCPOOL     → Use sync.Pool
KTN-VAR-ARRAY        → Use array if <=64 bytes
```

## PHASE 5 - MODERN (Go 1.18-1.26 idioms)

```text
KTN-VAR-USEANY       → interface{} → any
KTN-VAR-USECLEAR     → delete loop → clear()
KTN-VAR-USEMINMAX    → math.Min/Max → min/max
KTN-VAR-RANGEINT     → for i := 0; i < n → for i := range n
KTN-VAR-LOOPVAR      → Remove loop variable copy (Go 1.22+)
KTN-VAR-SLICEGROW    → Use slices.Grow
KTN-VAR-SLICECLONE   → Use slices.Clone
KTN-VAR-MAPCLONE     → Use maps.Clone
KTN-VAR-CMPOR        → Use cmp.Or
KTN-VAR-WGGO         → Use WaitGroup.Go (Go 1.25+)
KTN-FUNC-MINMAX      → math.Min/Max → min/max
KTN-FUNC-USECLEAR    → clear() builtin
KTN-FUNC-RANGEINT    → range over int
MODERNIZE-*          → All modernize rules
```

## PHASE 6 - STYLE (naming conventions)

```text
KTN-VAR-CAMEL        → snake_case → camelCase
KTN-CONST-CAMEL      → UPPER_CASE → UpperCase
KTN-VAR-MINLEN       → Rename var too short
KTN-VAR-MAXLEN       → Rename var too long
KTN-CONST-MINLEN     → Rename const too short
KTN-CONST-MAXLEN     → Rename const too long
KTN-FUNC-UNUSEDARG   → Prefix _ if unused
KTN-FUNC-BLANKPARAM  → Remove _ if not interface
KTN-FUNC-NOMAGIC     → Extract magic number into const
KTN-FUNC-EARLYRET    → Remove else after return
KTN-FUNC-NAKEDRET    → Add explicit return
KTN-STRUCT-NOGET     → GetX() → X()
KTN-INTERFACE-ERNAME → Add -er suffix
```

## PHASE 7 - DOCS (documentation - LAST)

```text
KTN-COMMENT-PKGDOC   → Add package doc
KTN-COMMENT-FUNC     → Add function doc
KTN-COMMENT-STRUCT   → Add struct doc
KTN-COMMENT-CONST    → Add const doc
KTN-COMMENT-VAR      → Add var doc
KTN-COMMENT-BLOCK    → Add block comment
KTN-COMMENT-LINELEN  → Wrap line >100 chars
KTN-GOROUTINE-LIFECYCLE → Document goroutine lifecycle
```

## PHASE 8 - TESTS (test patterns)

```text
KTN-TEST-TABLE       → Convert to table-driven
KTN-TEST-COVERAGE    → Add missing tests
KTN-TEST-ASSERT      → Add assertions
KTN-TEST-ERRCASES    → Add error cases
KTN-TEST-NOSKIP      → Remove t.Skip()
KTN-TEST-SETENV      → Fix t.Setenv in parallel
KTN-TEST-SUBPARALLEL → Add t.Parallel to subtests
KTN-TEST-CLEANUP     → Use t.Cleanup
```
