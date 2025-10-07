#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
gen_http_targets.py
生成 http-format 测试文件及对应 body json 文件。
每个请求包含随机 sha256 key 列表 + 随机 holder IP。
"""

import argparse
import json
import os
import random
import uuid
import hashlib

REQUEST_URL = "http://127.0.0.1:7789/api/v1/distribution/advertise"
HTTP_METHOD = "POST"
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


def make_body():
    holder_ip = random_ipv4()
    num_keys = random.randint(0, 1000)
    keys = [random_sha256() for _ in range(num_keys)]
    return {
        "keys": keys,
        "holder": f"{holder_ip}:{HOLDER_PORT}"
    }


def ensure_dir(path):
    os.makedirs(path, exist_ok=True)


def generate(outdir, num_targets):
    ensure_dir(outdir)
    bodies_dir = os.path.join(outdir, "bodies")
    ensure_dir(bodies_dir)
    targets_path = os.path.join(outdir, "targets.http")

    with open(targets_path, "w", encoding="utf-8") as tf:
        tf.write("# generated http-format targets\n\n")
        for i in range(num_targets):
            uid = uuid.uuid4().hex[:8]
            body_fname = f"body-{uid}.json"
            body_relpath = os.path.join("bodies", body_fname)
            body_disk_path = os.path.join(bodies_dir, body_fname)

            body_obj = make_body()
            with open(body_disk_path, "w", encoding="utf-8") as bf:
                json.dump(body_obj, bf, indent=4, ensure_ascii=False)

            tf.write(f"{HTTP_METHOD} {REQUEST_URL}\n")
            tf.write(f"Content-Type: {CONTENT_TYPE}\n")
            tf.write(f"@{body_relpath}\n\n")
            print(".", end="", flush=True)

    print()
    print(f"✅ 生成完成：{num_targets} 个 target，输出目录：{outdir}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="生成随机 http-format 测试文件")
    parser.add_argument("-o", "--outdir", default="./out_targets", help="输出目录")
    parser.add_argument("-n", "--number", type=int, default=5, help="要生成的 target 数量")
    args = parser.parse_args()

    generate(args.outdir, args.number)
