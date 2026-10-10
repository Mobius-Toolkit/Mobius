-- +goose NO TRANSACTION

UPDATE transcript
SET json = json_remove(json, '$.update.rawOutput', '$.update._meta.claudeCode.toolResponse')
WHERE json_extract(json, '$.update.sessionUpdate') = 'tool_call_update';

UPDATE transcript
SET json = json_remove(json, '$.update.availableCommands')
WHERE json_extract(json, '$.update.sessionUpdate') = 'available_commands_update';

VACUUM;

CREATE INDEX transcript_session ON transcript (session, id);
