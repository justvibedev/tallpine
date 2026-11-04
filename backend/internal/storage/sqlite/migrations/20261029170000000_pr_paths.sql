-- pr_paths records the files a pull request changes, as last observed from the
-- provider. It feeds the cross-lane merge digest (`tallpine merges`): which in-flight
-- PRs touch the same area. The provider caps the list, so it can be partial.

-- +goose Up
CREATE TABLE pr_paths (
    pr_url TEXT NOT NULL REFERENCES pr(url) ON DELETE CASCADE,
    path   TEXT NOT NULL,
    PRIMARY KEY (pr_url, path)
);

-- +goose Down
DROP TABLE pr_paths;
