#!/usr/bin/env python3
"""Open a local gateway without putting its bearer token in shell history."""
import argparse
import ipaddress
import json
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import quote


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--copy-token', action='store_true', help='Copy access token to clipboard instead of opening the console')
    args = parser.parse_args()
    config = json.loads(os.path.expandvars(args.config.expanduser().read_text()))
    host, port = config['listen'].rsplit(':', 1)
    address = host.strip('[]')
    if address != 'localhost' and not ipaddress.ip_address(address).is_loopback:
        raise ValueError('This launcher is for a loopback listener; use your HTTPS entry point for remote access.')
    token = config['token']
    if not token or '$' in token:
        raise ValueError('Access token is missing or its environment variable is unavailable.')
    if args.copy_token:
        subprocess.run(['/usr/bin/pbcopy'], input=token.encode(), check=True)
        print('Access token copied to clipboard.')
        return
    scheme = 'https' if config.get('tls_cert') else 'http'
    url = f'{scheme}://{host}:{int(port)}/console#token={quote(token, safe="")}'
    # /console removes the fragment immediately and keeps auth in sessionStorage.
    subprocess.run(['/usr/bin/open', url], check=True)
    print(f'Opened {scheme}://{host}:{int(port)}/console')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print(f'Cannot open console: {error}', file=sys.stderr)
        sys.exit(1)
