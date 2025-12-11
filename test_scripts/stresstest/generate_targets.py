#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
gen_http_targets.py
Generate http-format test files and corresponding body json files.
Contains advertise, findkey, and sync requests.
- Number of findkey requests is 10x of advertise
- Number of sync requests is 1/20 of findkey, simulating burst high QPS
- findkey: 70% probability uses already advertised keys, 30% uses random keys
- sync: 30% probability uses existing keys, 70% uses new keys, key length between 100-10000
"""

import argparse
import hashlib
import json
import os
import random
import string
import uuid

ADVERTISE_URL = "http://127.0.0.1:7789/api/v1/distribution/advertise"
FINDKEY_URL = "http://127.0.0.1:7789/api/v1/distribution/findkey"
SYNC_URL = "http://127.0.0.1:7789/api/v1/distribution/sync"
ADVERTISE_METHOD = "POST"
FINDKEY_METHOD = "GET"
SYNC_METHOD = "POST"
CONTENT_TYPE = "application/json"
HOLDER_PORT = 5123


def random_ipv4():
    """Generate a random IPv4 address (avoiding reserved segments)"""
    a = "10"
    b = random.randint(0, 255)
    c = random.randint(0, 255)
    d = random.randint(1, 254)
    return f"{a}.{b}.{c}.{d}"


def random_sha256():
    """Generate random sha256:<64hex>"""
    random_bytes = os.urandom(32)
    digest = hashlib.sha256(random_bytes).hexdigest()
    return f"sha256:{digest}"

def make_sync_body(existing_keys=None):
    """Generate body for sync request"""
    holder_ip = random_ipv4()
    num_keys = random.randint(50, 2000)  # sync usually has more keys
    
    keys = []
    for _ in range(num_keys):
        # 30% probability uses existing keys, 70% uses new keys
        if random.random() < 0.3 and existing_keys:
            key = random.choice(existing_keys)
        else:
            key = random_sha256()
        keys.append(key)
    
    return {
        "keys": keys,
        "holder": f"{holder_ip}:{HOLDER_PORT}"
    }


def make_advertise_body():
    """Generate body for advertise request"""
    holder_ip = random_ipv4()
    num_keys = random.randint(0, 1000)
    keys = [random_sha256() for _ in range(num_keys)]
    return {
        "keys": keys,
        "holder": f"{holder_ip}:{HOLDER_PORT}"
    }


def make_findkey_url(key, count=10):
    """Generate URL for findkey request"""
    return f"{FINDKEY_URL}?key={key}&count={count}"


def ensure_dir(path):
    os.makedirs(path, exist_ok=True)


def generate(outdir, num_advertise_targets):
    ensure_dir(outdir)
    bodies_dir = os.path.join(outdir, "bodies")
    ensure_dir(bodies_dir)
    targets_path = os.path.join(outdir, "targets.http")

    # Store all advertised keys for subsequent findkey and sync requests
    advertised_keys = []
    
    # Calculate request quantities
    num_findkey_targets = num_advertise_targets * 10
    num_sync_targets = max(1, num_findkey_targets // 20)  # sync is 1/20 of findkey
    
    # Create list of all requests for mixed generation
    all_requests = []
    
    # Add all request types
    for i in range(num_advertise_targets):
        all_requests.append(('advertise', i))
    for i in range(num_findkey_targets):
        all_requests.append(('findkey', i))
    for i in range(num_sync_targets):
        all_requests.append(('sync', i))
    
    # Randomly shuffle all requests
    random.shuffle(all_requests)

    with open(targets_path, "w", encoding="utf-8") as tf:
        tf.write("# generated http-format targets\n")
        tf.write("# Contains mixed advertise, findkey, and sync requests\n\n")
        
        total_requests = len(all_requests)
        
        # Batch processing to reduce file I/O
        body_files_to_write = []
        
        for i, (req_type, req_idx) in enumerate(all_requests):
            if req_type == 'advertise':
                uid = uuid.uuid4().hex[:8]
                body_fname = f"advertise-{uid}.json"
                body_relpath = os.path.join("bodies", body_fname)
                body_disk_path = os.path.join(bodies_dir, body_fname)

                body_obj = make_advertise_body()
                # Collect keys for subsequent findkey and sync requests
                advertised_keys.extend(body_obj["keys"])
                
                # Collect files to write
                body_files_to_write.append((body_disk_path, body_obj))

                tf.write(f"{ADVERTISE_METHOD} {ADVERTISE_URL}\n")
                tf.write(f"Content-Type: {CONTENT_TYPE}\n")
                tf.write(f"@{body_relpath}\n\n")
                if (i + 1) % 100 == 0:
                    print(".", end="", flush=True)
                
            elif req_type == 'findkey':
                # 70% probability uses already advertised keys, 30% uses random keys
                if random.random() < 0.7 and advertised_keys:
                    # Use already advertised key
                    key = random.choice(advertised_keys)
                else:
                    # Use randomly generated key
                    key = random_sha256()
                
                count = random.randint(1, 20)  # Random count parameter
                url = make_findkey_url(key, count)
                
                tf.write(f"{FINDKEY_METHOD} {url}\n")
                tf.write(f"Accept: application/json\n\n")
                if (i + 1) % 100 == 0:
                    print("*", end="", flush=True)
                
            elif req_type == 'sync':
                uid = uuid.uuid4().hex[:8]
                body_fname = f"sync-{uid}.json"
                body_relpath = os.path.join("bodies", body_fname)
                body_disk_path = os.path.join(bodies_dir, body_fname)

                body_obj = make_sync_body(advertised_keys)
                
                # Collect files to write
                body_files_to_write.append((body_disk_path, body_obj))

                tf.write(f"{SYNC_METHOD} {SYNC_URL}\n")
                tf.write(f"Content-Type: {CONTENT_TYPE}\n")
                tf.write(f"@{body_relpath}\n\n")
                if (i + 1) % 100 == 0:
                    print("#", end="", flush=True)
        
        # Batch write all JSON files
        print("\nWriting JSON files...", end="", flush=True)
        for body_disk_path, body_obj in body_files_to_write:
            with open(body_disk_path, "w", encoding="utf-8") as bf:
                json.dump(body_obj, bf, separators=(',', ':'), ensure_ascii=False)
        print(" Done!")

    print()
    print(f"✅ Generation completed:")
    print(f"   - {num_advertise_targets} advertise requests")
    print(f"   - {num_findkey_targets} findkey requests")
    print(f"   - {num_sync_targets} sync requests")
    print(f"   - Total {len(advertised_keys)} advertised keys")
    print(f"   - Output directory: {outdir}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Generate random http-format test files")
    parser.add_argument("-o", "--outdir", default="./out_targets", help="Output directory")
    parser.add_argument("-n", "--advertise-number", type=int, default=5, 
                       help="Number of advertise requests to generate (findkey requests will be 10x this number, sync requests will be 1/20 of findkey)")
    args = parser.parse_args()

    generate(args.outdir, args.advertise_number)
