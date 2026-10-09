"""Checks Hysteria2 servers from diag.json with the official hysteria client."""
import json, subprocess, sys, time, urllib.parse as up
nodes = [n for n in json.load(open("diag.json")) if n["protocol"] == "Hysteria2"]
HY = sys.argv[1]
lines = []
for i, n in enumerate(nodes):
    u = up.urlsplit(n["link"])
    q = dict(up.parse_qsl(u.query))
    auth = up.unquote(n["link"].split("://", 1)[1].split("@", 1)[0])
    port = 21000 + i
    cfg = (f'server: "{n["host"]}:{n["port"]}"\nauth: "{auth}"\n'
           f'tls:\n  sni: "{q.get("sni", "")}"\n  insecure: {"true" if q.get("insecure") == "1" else "false"}\n'
           f'socks5:\n  listen: 127.0.0.1:{port}\n')
    open(f"hy{i}.yaml", "w").write(cfg)
    p = subprocess.Popen([HY, "client", "-c", f"hy{i}.yaml"], stdout=open(f"hy{i}.log", "w"), stderr=subprocess.STDOUT)
    time.sleep(3)
    r = subprocess.run(["curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "10", "--socks5-hostname",
                        f"127.0.0.1:{port}", "https://www.gstatic.com/generate_204"], capture_output=True, text=True)
    p.terminate()
    time.sleep(0.5)
    log = open(f"hy{i}.log").read().replace(auth, "***")[-400:].replace("\n", " ")
    lines.append(f'{n["host"]}:{n["port"]} hysteria={r.stdout.strip()} vela={"ok" if n["delay"] > 0 else n["error"][:60]} | {log}')
print("\n".join(lines))
print("::notice title=hysteria2::" + "%0A".join(lines))
