"""Fixed sandbox entrypoint. Never run this outside the isolated container."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time

def main():
    process = subprocess.Popen(
        [sys.executable, '-I', '-B', sys.argv[1]],
        cwd='/workspace', stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    summary = bytearray()
    while True:
        chunk = process.stdout.read(4096)
        if not chunk:
            break
        remaining = 8 * 1024 - len(summary)
        if remaining > 0:
            summary.extend(chunk[:remaining])
    exit_code = process.wait()
    # Stop subprocess descendants that remain in its process group.
    try:
        import signal
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    value = {'done': True, 'exit': exit_code,
             'summary': summary.decode('utf-8', errors='replace')}
    temporary = Path('/tmp/status.tmp')
    temporary.write_text(json.dumps(value, ensure_ascii=True))
    temporary.replace('/tmp/status.json')
    # Worker copies tmpfs artifacts before removing this container.
    while True:
        time.sleep(60)

if __name__ == '__main__':
    main()
