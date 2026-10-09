"""Checks the same servers with Xray-core and prints a comparison.
Reads diag.json (from tools/diag), writes annotations without keys."""
import json, os, subprocess, sys, time, collections, urllib.parse as up
from concurrent.futures import ThreadPoolExecutor

nodes = json.load(open("diag.json"))
XRAY = sys.argv[1]

def outbound(n, tag):
    u = up.urlsplit(n["link"])
    q = dict(up.parse_qsl(u.query))
    if u.scheme != "vless":
        return None
    stream = {"network": q.get("type", "tcp"), "security": q.get("security", "none")}
    if stream["security"] == "reality":
        stream["realitySettings"] = {"serverName": q.get("sni", ""), "fingerprint": q.get("fp", "chrome"),
                                     "publicKey": q.get("pbk", ""), "shortId": q.get("sid", ""), "spiderX": q.get("spx", "")}
    elif stream["security"] == "tls":
        t = {"serverName": q.get("sni", ""), "fingerprint": q.get("fp", "chrome")}
        if q.get("alpn"):
            t["alpn"] = q["alpn"].split(",")
        if q.get("pcs"):
            t["pinnedPeerCertSha256"] = q["pcs"]
        if q.get("allowInsecure") in ("1", "true"):
            t["allowInsecure"] = True
        stream["tlsSettings"] = t
    if stream["network"] == "grpc":
        stream["grpcSettings"] = {"serviceName": q.get("serviceName", ""), "multiMode": q.get("mode") == "multi",
                                  "authority": q.get("authority", "")}
    elif stream["network"] in ("ws",):
        stream["wsSettings"] = {"path": q.get("path", "/"), "host": q.get("host", "")}
    return {"tag": tag, "protocol": "vless",
            "settings": {"vnext": [{"address": u.hostname, "port": u.port or 443,
                                    "users": [{"id": up.unquote(u.username or ""), "encryption": q.get("encryption", "none"),
                                               "flow": q.get("flow", "")}]}]},
            "streamSettings": stream}

inb, outb, rules, ports = [], [], [], {}
for i, n in enumerate(nodes):
    ob = outbound(n, f"o{i}")
    if not ob:
        continue
    port = 20000 + i
    ports[i] = port
    inb.append({"tag": f"i{i}", "listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": {"udp": False}})
    outb.append(ob)
    rules.append({"type": "field", "inboundTag": [f"i{i}"], "outboundTag": f"o{i}"})
cfg = {"log": {"loglevel": "error"}, "inbounds": inb, "outbounds": outb + [{"protocol": "freedom", "tag": "direct"}],
       "routing": {"rules": rules}}
json.dump(cfg, open("xray.json", "w"))
proc = subprocess.Popen([XRAY, "run", "-c", "xray.json"], stdout=open("xray.log", "w"), stderr=subprocess.STDOUT)
time.sleep(3)

hostsem = collections.defaultdict(lambda: __import__("threading").Semaphore(2))
def test(i):
    n = nodes[i]
    with hostsem[n["host"]]:
        t = time.time()
        r = subprocess.run(["curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "10",
                            "--socks5-hostname", f"127.0.0.1:{ports[i]}", "https://www.gstatic.com/generate_204"],
                           capture_output=True, text=True)
        ok = r.stdout.strip() == "204"
        if not ok:  # second calm try, like Vela
            time.sleep(1)
            r = subprocess.run(["curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "10",
                                "--socks5-hostname", f"127.0.0.1:{ports[i]}", "https://www.gstatic.com/generate_204"],
                               capture_output=True, text=True)
            ok = r.stdout.strip() == "204"
        return i, ok, int((time.time() - t) * 1000)

with ThreadPoolExecutor(16) as ex:
    xres = {i: (ok, ms) for i, ok, ms in ex.map(test, list(ports))}
proc.terminate()

def key(n):
    return f'{n["host"]}:{n["port"]} {n["security"]}/{n["network"] or ""} fp={n["fp"]}'
groups = collections.defaultdict(lambda: collections.Counter())
reasons = collections.defaultdict(collections.Counter)
for i, n in enumerate(nodes):
    k = key(n)
    v = n["delay"] > 0
    x = xres.get(i, (None, 0))[0]
    groups[k]["total"] += 1
    groups[k]["vela"] += v
    groups[k]["tcp"] += n["tcp"] > 0
    if x is not None:
        groups[k]["xray"] += x
        groups[k]["xtested"] += 1
    if not v:
        reasons[k][(n["error"] or "")[:110]] += 1
lines = []
tot = collections.Counter()
for k, c in sorted(groups.items(), key=lambda kv: -kv[1]["total"]):
    tot.update(c)
    lines.append(f'{k}: total {c["total"]}, tcp {c["tcp"]}, vela {c["vela"]}, xray {c["xray"]}/{c["xtested"]}')
    for r, cnt in reasons[k].most_common(2):
        lines.append(f'    vela err x{cnt}: {r}')
summary = f'TOTAL {tot["total"]}: tcp {tot["tcp"]}, vela {tot["vela"]}, xray {tot["xray"]}/{tot["xtested"]}'
print(summary)
print("\n".join(lines))
# annotations: one per ~40 lines (they are truncated otherwise)
print(f"::notice title=summary::{summary}")
for j in range(0, len(lines), 30):
    body = "%0A".join(lines[j:j + 30])
    print(f"::notice title=groups {j // 30 + 1}::{body}")
mism = [nodes[i] for i in xres if xres[i][0] and nodes[i]["delay"] <= 0]
body = "%0A".join(f'{key(n)} | {(n["error"] or "")[:150]}' for n in mism[:25])
print(f"::notice title=xray ok, vela fails ({len(mism)})::{body}")
xl = open("xray.log").read()[-3000:].replace("\n", "%0A")
print(f"::notice title=xray log::{xl}")
