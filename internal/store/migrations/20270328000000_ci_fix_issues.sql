CREATE VIEW ci_fix_issues AS
SELECT repository, CAST(ABS(issue) AS INTEGER) AS issue FROM ci_breaks
UNION
SELECT repository, CAST(ABS(issue) AS INTEGER) AS issue FROM ci_failures WHERE issue <> 0;
