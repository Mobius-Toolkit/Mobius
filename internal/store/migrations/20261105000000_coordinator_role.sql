UPDATE sessions SET role = 'coordinator' WHERE role = 'triager' AND repository = '';
UPDATE chat_messages SET author = 'Coordinator' WHERE author = 'Triager';
