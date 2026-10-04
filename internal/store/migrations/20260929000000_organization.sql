-- A row of the Triager chat has an organization and the empty repository.
ALTER TABLE sessions ADD COLUMN organization TEXT NOT NULL DEFAULT '';
UPDATE sessions SET organization = substr(repository, 1, instr(repository, '/') - 1)
WHERE instr(repository, '/') > 0;
UPDATE sessions SET organization = repository, repository = ''
WHERE instr(repository, '/') = 0;

ALTER TABLE chat_messages ADD COLUMN organization TEXT NOT NULL DEFAULT '';
UPDATE chat_messages SET organization = substr(repository, 1, instr(repository, '/') - 1)
WHERE instr(repository, '/') > 0;
UPDATE chat_messages SET organization = repository, repository = ''
WHERE instr(repository, '/') = 0;

ALTER TABLE inbox_items ADD COLUMN organization TEXT NOT NULL DEFAULT '';
UPDATE inbox_items SET organization = substr(repository, 1, instr(repository, '/') - 1)
WHERE instr(repository, '/') > 0;
UPDATE inbox_items SET organization = repository, repository = ''
WHERE instr(repository, '/') = 0;

CREATE TABLE chat_seen_new (
    organization TEXT NOT NULL,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    message INTEGER NOT NULL,
    PRIMARY KEY (organization, repository, workstream)
);
INSERT INTO chat_seen_new
SELECT substr(repository, 1, instr(repository, '/') - 1), repository, workstream, message
FROM chat_seen WHERE instr(repository, '/') > 0;
INSERT INTO chat_seen_new
SELECT repository, '', workstream, message
FROM chat_seen WHERE instr(repository, '/') = 0;
DROP TABLE chat_seen;
ALTER TABLE chat_seen_new RENAME TO chat_seen;
