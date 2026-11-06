#!/usr/bin/env python3
"""Exercise one running project. Run from the repository root after setup.py."""
import argparse
import datetime as dt
import json
from pathlib import Path
import time
import urllib.request
import urllib.error

parser = argparse.ArgumentParser()
parser.add_argument('project', choices=['ordercore'])
parser.add_argument('--url', default='http://localhost:8084')
args = parser.parse_args()
values = dict(line.split('=',1) for line in (Path(__file__).resolve().parent.parent/'.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
token = values['ADMIN_TOKEN']


def call(method, path, body=None):
    req = urllib.request.Request(args.url+path, data=None if body is None else json.dumps(body).encode(), method=method,
        headers={'Authorization': 'Bearer '+token,'Content-Type':'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=20) as response:
            result = json.load(response)
    except urllib.error.HTTPError as error:
        raise SystemExit(f'{method} {path}: {error.code} {error.read().decode()}')
    print(method, path, json.dumps(result, ensure_ascii=False))
    return result


project=args.project
if project=='ordercore':
    product=call('POST','/products',{'name':'Клавиатура','price_cents':500000,'stock':2})
    order=call('POST','/orders',{'items':[{'product_id':product['id'],'quantity':1}]})
    call('POST','/orders/'+order['id']+'/payment',{'event_id':'demo-'+order['id'],'outcome':'success'})
