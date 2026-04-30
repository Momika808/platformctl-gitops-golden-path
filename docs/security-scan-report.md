# Security Scan Report

- tool: gitleaks
- timestamp_utc: 2026-04-30T15:58:02Z
- mode: filesystem scan (--no-git --redact)
- exit_code: 0
- findings_count: 0

## Command

    /Users/robot/go/bin/gitleaks detect --source . --no-git --redact --report-format json --report-path docs/gitleaks-report.json

## Notes

- Report file: docs/gitleaks-report.json
- This run scans current filesystem content of the repository.
- CI also runs secret scan via GitHub Actions (gitleaks/gitleaks-action@v2).
