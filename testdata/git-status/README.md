# Porcelain v2 fixtures

The `.nul` files are actual output from Git 2.43.0 in disposable repositories,
captured with `status --porcelain=v2 --branch -z --untracked-files=all
--ignore-submodules=none`. They contain binary NUL record delimiters, including
rename source records; object IDs vary on regeneration.

Regenerate deliberately with `GITPERCH_UPDATE_FIXTURES=1 go test
./internal/git -run TestRealStatusFixtures`. Normal tests only read these files
and separately create fresh repositories to validate current Git behavior.
