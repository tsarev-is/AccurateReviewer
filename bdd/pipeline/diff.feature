@diff
Feature: Diff parsing and review-unit construction
  As the AccurateReviewer pipeline
  I must reason about changed lines only, with a small amount of surrounding context
  Because reviewing the entire file would either drown the LLM in noise
  Or generate comments on legacy code that the PR author did not touch

  A "review unit" is one parsed file from the diff: the changed hunks,
  N lines of context above and below each hunk, the file's path, and
  enough metadata for the master agent to decide which workers to run.

  Background:
    Given the accurate-reviewer binary is on PATH

  Scenario: A single-file unified diff produces one review unit
    Given a unified diff:
      """
      diff --git a/auth.go b/auth.go
      index 1111111..2222222 100644
      --- a/auth.go
      +++ b/auth.go
      @@ -10,3 +10,5 @@ func Authenticate(user, pass string) bool {
           if user == "" { return false }
      +    if pass == "admin" { return true }
      +    log.Printf("login attempt: user=%s pass=%s", user, pass)
           return checkHash(user, pass)
       }
      """
    When I run "accurate-reviewer parse-diff -"
    Then the exit code is 0
    And the parsed output contains exactly 1 review unit
    And review unit 0 has:
      | field            | value     |
      | file             | auth.go   |
      | added_lines      | 2         |
      | removed_lines    | 0         |
      | hunks            | 1         |

  Scenario: Multiple files produce multiple review units
    Given a unified diff:
      """
      diff --git a/a.go b/a.go
      --- a/a.go
      +++ b/a.go
      @@ -1,1 +1,2 @@
       package a
      +var X = 1
      diff --git a/b.go b/b.go
      --- a/b.go
      +++ b/b.go
      @@ -1,1 +1,2 @@
       package b
      +var Y = 2
      """
    When I run "accurate-reviewer parse-diff -"
    Then the parsed output contains exactly 2 review units
    And the files of the review units are:
      | file |
      | a.go |
      | b.go |

  Scenario: Context lines are attached to each hunk
    # Three lines of context above and below each hunk, capped at file edges.
    # The LLM needs enough surrounding code to understand the change without
    # being given the whole file.
    Given a unified diff:
      """
      diff --git a/x.go b/x.go
      --- a/x.go
      +++ b/x.go
      @@ -5,7 +5,8 @@
        line 4
        line 5
        line 6
      + line NEW
        line 7
        line 8
        line 9
      """
    When I run "accurate-reviewer parse-diff -"
    Then review unit 0's hunk 0 has 3 context lines before the change
    And review unit 0's hunk 0 has 3 context lines after the change

  Scenario: Excluded paths are dropped from the review-unit list
    Given a .review.yml that excludes "vendor/**"
    And a unified diff:
      """
      diff --git a/vendor/lib/foo.go b/vendor/lib/foo.go
      --- a/vendor/lib/foo.go
      +++ b/vendor/lib/foo.go
      @@ -1,1 +1,2 @@
       package lib
      +var Z = 3
      diff --git a/src/main.go b/src/main.go
      --- a/src/main.go
      +++ b/src/main.go
      @@ -1,1 +1,2 @@
       package main
      +var Q = 4
      """
    When I run "accurate-reviewer parse-diff -"
    Then the parsed output contains exactly 1 review unit
    And review unit 0's file is "src/main.go"

  Scenario: A pure deletion does not produce a reviewable unit
    # We only review additions and modifications. A file deletion contains
    # no new code for the LLM to inspect.
    Given a unified diff:
      """
      diff --git a/dead.go b/dead.go
      deleted file mode 100644
      --- a/dead.go
      +++ /dev/null
      @@ -1,3 +0,0 @@
      -package dead
      -
      -var Old = 1
      """
    When I run "accurate-reviewer parse-diff -"
    Then the parsed output contains exactly 0 review units

  Scenario: A binary diff is skipped without error
    Given a unified diff:
      """
      diff --git a/logo.png b/logo.png
      index 0000000..1111111 100644
      Binary files a/logo.png and b/logo.png differ
      """
    When I run "accurate-reviewer parse-diff -"
    Then the exit code is 0
    And the parsed output contains exactly 0 review units

  Scenario: Diff input from a file works the same as stdin
    Given a unified diff stored at "testdata/diffs/single_file.diff":
      """
      diff --git a/a.go b/a.go
      --- a/a.go
      +++ b/a.go
      @@ -1,1 +1,2 @@
       package a
      +var X = 1
      """
    When I run "accurate-reviewer parse-diff testdata/diffs/single_file.diff"
    Then the parsed output contains exactly 1 review unit
    And review unit 0's file is "a.go"

  Scenario: Renames are reported with old and new paths
    Given a unified diff:
      """
      diff --git a/old.go b/new.go
      similarity index 95%
      rename from old.go
      rename to new.go
      --- a/old.go
      +++ b/new.go
      @@ -1,2 +1,3 @@
       package main
       func A() {}
      +func B() {}
      """
    When I run "accurate-reviewer parse-diff -"
    Then the parsed output contains exactly 1 review unit
    And review unit 0 has:
      | field    | value  |
      | file     | new.go |
      | old_file | old.go |
      | renamed  | true   |
