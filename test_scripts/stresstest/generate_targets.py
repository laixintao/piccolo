#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
gen_http_targets.py
生成 http-format 测试文件及对应 body json 文件。
包含 advertise、findkey 和 sync 请求。
- findkey 请求数量是 advertise 的10倍
- sync 请求数量是 findkey 的1/20，但会模拟突发高 QPS
- findkey: 70%概率使用已 advertise 的 key，30%使用随机 key
- sync: 30%概率使用已有的 key，70%使用新 key，key 长度在100-10000之间
"""

import argparse
import json
import os
import random
import uuid
import hashlib
import string

ADVERTISE_URL = "http://127.0.0.1:7789/api/v1/distribution/advertise"
FINDKEY_URL = "http://127.0.0.1:7789/api/v1/distribution/findkey"
SYNC_URL = "http://127.0.0.1:7789/api/v1/distribution/sync"
ADVERTISE_METHOD = "POST"
FINDKEY_METHOD = "GET"
SYNC_METHOD = "POST"
CONTENT_TYPE = "application/json"
HOLDER_PORT = 5123


def random_ipv4():
    """生成一个随机 IPv4 地址（避免保留网段）"""
    a = "10"
    b = random.randint(0, 255)
    c = random.randint(0, 255)
    d = random.randint(1, 254)
    return f"{a}.{b}.{c}.{d}"


def random_sha256():
    """生成随机 sha256:<64hex>"""
    random_bytes = os.urandom(32)
    digest = hashlib.sha256(random_bytes).hexdigest()
    return f"sha256:{digest}"


def random_sync_key(min_length=100, max_length=10000):
    """生成随机 sync key，长度在指定范围内"""
    length = random.randint(min_length, max_length)
    # 使用字母、数字和常见符号生成随机字符串
    chars = string.ascii_letters + string.digits + "._-"
    return ''.join(random.choice(chars) for _ in range(length))


def make_sync_body(existing_keys=None):
    """生成 sync 请求的 body"""
    holder_ip = random_ipv4()
    num_keys = random.randint(50, 2000)  # sync 通常有更多 keys
    
    keys = []
    for _ in range(num_keys):
        # 30% 概率使用已有的 key，70% 使用新 key
        if random.random() < 0.3 and existing_keys:
            key = random.choice(existing_keys)
        else:
            key = random_sync_key()
        keys.append(key)
    
    return {
        "keys": keys,
        "holder": f"{holder_ip}:{HOLDER_PORT}"
    }


def make_advertise_body():
    """生成 advertise 请求的 body"""
    holder_ip = random_ipv4()
    num_keys = random.randint(0, 1000)
    keys = [random_sha256() for _ in range(num_keys)]
    return {
        "keys": keys,
        "holder": f"{holder_ip}:{HOLDER_PORT}"
    }


def make_findkey_url(key, count=10):
    """生成 findkey 请求的 URL"""
    return f"{FINDKEY_URL}?key={key}&count={count}"


def ensure_dir(path):
    os.makedirs(path, exist_ok=True)


def generate(outdir, num_advertise_targets):
    ensure_dir(outdir)
    bodies_dir = os.path.join(outdir, "bodies")
    ensure_dir(bodies_dir)
    targets_path = os.path.join(outdir, "targets.http")

    # 存储所有 advertise 的 keys，用于后续的 findkey 和 sync 请求
    advertised_keys = []
    
    # 计算请求数量
    num_findkey_targets = num_advertise_targets * 10
    num_sync_targets = max(1, num_findkey_targets // 20)  # sync 是 findkey 的 1/20
    
    # 模拟突发 QPS：将 sync 请求集中在几个时间段内
    burst_periods = 3
    sync_per_burst = num_sync_targets // burst_periods
    remaining_sync = num_sync_targets % burst_periods
    
    # 创建请求序列：先随机混合，然后调整 sync 请求的位置
    all_requests = []
    
    # 添加所有请求类型
    for i in range(num_advertise_targets):
        all_requests.append(('advertise', i))
    for i in range(num_findkey_targets):
        all_requests.append(('findkey', i))
    for i in range(num_sync_targets):
        all_requests.append(('sync', i))
    
    # 随机打乱
    random.shuffle(all_requests)
    
    # 重新组织 sync 请求，让它们在突发时间段内集中
    sync_requests = [(req_type, req_idx) for req_type, req_idx in all_requests if req_type == 'sync']
    other_requests = [(req_type, req_idx) for req_type, req_idx in all_requests if req_type != 'sync']
    
    # 将其他请求分成几段，在每段之间插入 sync 突发
    segment_size = len(other_requests) // burst_periods
    mixed_requests = []
    
    sync_idx = 0
    for burst_idx in range(burst_periods):
        # 添加一段其他请求
        start_idx = burst_idx * segment_size
        if burst_idx == burst_periods - 1:  # 最后一段包含所有剩余请求
            end_idx = len(other_requests)
        else:
            end_idx = start_idx + segment_size
        
        mixed_requests.extend(other_requests[start_idx:end_idx])
        
        # 添加这个突发时间段的 sync 请求
        burst_size = sync_per_burst
        if burst_idx < remaining_sync:
            burst_size += 1
        
        for i in range(burst_size):
            if sync_idx < len(sync_requests):
                mixed_requests.append(sync_requests[sync_idx])
                sync_idx += 1

    with open(targets_path, "w", encoding="utf-8") as tf:
        tf.write("# generated http-format targets\n")
        tf.write("# 包含混合的 advertise、findkey 和 sync 请求\n")
        tf.write("# sync 请求会模拟突发高 QPS 情况\n\n")
        
        sync_count = 0
        current_burst = 0
        total_requests = len(mixed_requests)
        
        for i, (req_type, req_idx) in enumerate(mixed_requests):
            if req_type == 'advertise':
                uid = uuid.uuid4().hex[:8]
                body_fname = f"advertise-{uid}.json"
                body_relpath = os.path.join("bodies", body_fname)
                body_disk_path = os.path.join(bodies_dir, body_fname)

                body_obj = make_advertise_body()
                # 收集 keys 用于后续的 findkey 和 sync 请求
                advertised_keys.extend(body_obj["keys"])
                
                with open(body_disk_path, "w", encoding="utf-8") as bf:
                    json.dump(body_obj, bf, indent=4, ensure_ascii=False)

                tf.write(f"{ADVERTISE_METHOD} {ADVERTISE_URL}\n")
                tf.write(f"Content-Type: {CONTENT_TYPE}\n")
                tf.write(f"@{body_relpath}\n\n")
                if (i + 1) % 100 == 0:
                    print(".", end="", flush=True)
                
            elif req_type == 'findkey':
                # 70% 概率使用已 advertise 的 key，30% 使用随机 key
                if random.random() < 0.7 and advertised_keys:
                    # 使用已 advertise 的 key
                    key = random.choice(advertised_keys)
                else:
                    # 使用随机生成的 key
                    key = random_sha256()
                
                count = random.randint(1, 20)  # 随机 count 参数
                url = make_findkey_url(key, count)
                
                tf.write(f"{FINDKEY_METHOD} {url}\n")
                tf.write(f"Accept: application/json\n\n")
                if (i + 1) % 100 == 0:
                    print("*", end="", flush=True)
                
            elif req_type == 'sync':
                # 检查是否需要开始新的突发时间段
                if sync_count == 0:
                    current_burst += 1
                    tf.write(f"\n# 突发时间段 {current_burst}\n")
                
                uid = uuid.uuid4().hex[:8]
                body_fname = f"sync-{uid}.json"
                body_relpath = os.path.join("bodies", body_fname)
                body_disk_path = os.path.join(bodies_dir, body_fname)

                body_obj = make_sync_body(advertised_keys)
                
                with open(body_disk_path, "w", encoding="utf-8") as bf:
                    json.dump(body_obj, bf, indent=4, ensure_ascii=False)

                tf.write(f"{SYNC_METHOD} {SYNC_URL}\n")
                tf.write(f"Content-Type: {CONTENT_TYPE}\n")
                tf.write(f"@{body_relpath}\n\n")
                print("#", end="", flush=True)
                sync_count += 1
                
                # 检查突发时间段是否结束
                expected_burst_size = sync_per_burst
                if current_burst <= remaining_sync:
                    expected_burst_size += 1
                
                if sync_count >= expected_burst_size:
                    tf.write(f"# 突发时间段 {current_burst} 结束\n\n")
                    sync_count = 0

    print()
    print(f"✅ 生成完成：")
    print(f"   - {num_advertise_targets} 个 advertise 请求")
    print(f"   - {num_findkey_targets} 个 findkey 请求")
    print(f"   - {num_sync_targets} 个 sync 请求（分布在 {burst_periods} 个突发时间段）")
    print(f"   - 总共 {len(advertised_keys)} 个已 advertise 的 keys")
    print(f"   - 输出目录：{outdir}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="生成随机 http-format 测试文件")
    parser.add_argument("-o", "--outdir", default="./out_targets", help="输出目录")
    parser.add_argument("-n", "--advertise-number", type=int, default=5, 
                       help="要生成的 advertise 请求数量（findkey 请求将是此数量的10倍，sync 请求将是 findkey 的1/20）")
    args = parser.parse_args()

    generate(args.outdir, args.advertise_number)
