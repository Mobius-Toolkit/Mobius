ALTER TABLE events ADD COLUMN organization TEXT NOT NULL DEFAULT '';
UPDATE events SET organization = substr(repository, 1, instr(repository, '/') - 1);
