# Golden parquet-bitmap fixture

`unit_coverage_v1.parquet` and `unit_coverage_v1.json` are shared byte-for-byte
with the coverage server and the HCli producer. Do not edit them here.

The repos declare the parquet-bitmap row schema independently
(`UnitCoverageRecord` in this package, in HCli, and in harness-code). Nothing at
compile time keeps them in step, and a mismatched column tag does not fail
loudly: parquet decodes the unknown column into a zero value, so the server
would index every report as empty coverage. Each repo reads this same fixture and
asserts the same manifest, so drift fails a test in whichever repo moved.

The parquet file is checked in rather than regenerated at test time on purpose:
reading bytes written by an older `parquet-go` is the compatibility property
worth pinning.

Only the HCli producer regenerates the fixture; copy both files here and into
harness-code in the same change.
