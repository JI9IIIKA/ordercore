#!/usr/bin/env python3
"""Create local credentials once. Never overwrite an existing .env."""
from pathlib import Path
import json
import secrets
import os
root = Path(__file__).resolve().parent.parent
path = root / '.env'
if path.exists():
    raise SystemExit('.env already exists; left unchanged')
admin, editor, viewer = (secrets.token_hex(24) for _ in range(3))
keys = {admin: {'tenant': 'demo', 'user': 'alice', 'role': 'admin'},
        editor: {'tenant': 'demo', 'user': 'bob', 'role': 'editor'},
        viewer: {'tenant': 'demo', 'user': 'reader', 'role': 'viewer'}}
lines = ['PROJECT=ordercore', 'PORT=8084', f'ADMIN_TOKEN={admin}',
         f'EDITOR_TOKEN={editor}', f'VIEWER_TOKEN={viewer}',
         "API_KEYS_JSON='" + json.dumps(keys, separators=(',', ':')) + "'",
         'WEBHOOK_SECRET=' + secrets.token_hex(32),
         'AI_SERVICE_TOKEN=' + secrets.token_hex(32)]
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as f:
    f.write('\n'.join(lines) + '\n')
print('Created .env. Start: docker compose up --build -d')
