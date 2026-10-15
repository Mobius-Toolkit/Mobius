UPDATE chat_messages SET delivered_at = time
WHERE author = 'Owner' AND browser_id IS NULL AND delivered_at IS NULL AND stopped_at IS NULL;
