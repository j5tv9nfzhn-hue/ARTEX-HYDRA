# -*- coding: utf-8 -*-
import base64, io, os, sys, json, subprocess
B = io.StringIO()
def p(s=""): B.write(s + "\n")
def mb(n): return n/1048576.0
def dsize(path):
    t=f=0
    for dp,dns,fns in os.walk(path):
        for fn in fns:
            try: t+=os.path.getsize(os.path.join(dp,fn)); f+=1
            except OSError: pass
    return t,f
USER = os.environ.get("USERPROFILE", r"C:\Users\Administrator")
LA = os.path.join(USER,"AppData","Local")
UD = os.path.join(LA,"BraveSoftware","Brave-Browser","User Data")

p("### 安装体积")
pf = r"C:\Program Files\BraveSoftware"
t,f = dsize(pf); p("  C:\\Program Files\\BraveSoftware  %.0f MB / %d files" % (mb(t), f))

p("### 神秘组件目录 aoojcmojmmcbpfgoecoadbdpnagfchel")
d = os.path.join(UD, "aoojcmojmmcbpfgoecoadbdpnagfchel")
if os.path.isdir(d):
    for root,dirs,files in os.walk(d):
        for fn in files:
            if fn == "manifest.json":
                try:
                    j = json.load(io.open(os.path.join(root,fn),encoding="utf-8",errors="replace"))
                    p("  manifest: name=%s version=%s" % (j.get("name"), j.get("version")))
                except Exception as e: p("  manifest 读取失败 %s" % e)
    p("  顶层内容: %s" % os.listdir(d)[:10])
else:
    p("  (不存在)")

p("### 当前是否有 brave 进程在跑 / 内存")
try:
    out = subprocess.run(["tasklist","/FI","IMAGENAME eq brave.exe","/FO","CSV","/NH"],
                         capture_output=True, text=True, errors="replace").stdout
    lines = [l for l in out.splitlines() if "brave.exe" in l.lower()]
    p("  brave.exe 进程数: %d" % len(lines))
    tot=0
    for l in lines[:40]:
        parts = [x.strip('"') for x in l.split('","')]
        if len(parts) >= 5:
            mem = parts[4].replace(" K","").replace(",","").replace(" ","")
            try: tot += int(mem)
            except: pass
    p("  合计内存(RAM): %.0f MB" % (tot/1024.0))
except Exception as e:
    p("  查询失败 %s" % e)

p("### Preferences 里 brave.* 顶层键（判断是否动过设置）")
pref = os.path.join(UD,"Default","Preferences")
if os.path.exists(pref):
    j = json.load(io.open(pref,encoding="utf-8",errors="replace"))
    brave = j.get("brave",{})
    p("  brave 下键: %s" % ", ".join(sorted(brave.keys())[:25]))
    p("  shields 内容: %s" % json.dumps(brave.get("shields",{}), ensure_ascii=False)[:400])
    # 关键开关
    p("  --- 关键开关抽查 ---")
    def g(path):
        cur = j
        for k in path.split("."):
            if not isinstance(cur, dict): return "(无)"
            cur = cur.get(k)
            if cur is None: return "(未设置=默认)"
        return cur
    for k in ["brave.stats.p3a","brave.stats.usage_ping","brave.stats.reporting",
              "brave.rewards.enabled","brave.wallet.should_show_wallet",
              "brave.background_metrics","brave.tor.enabled",
              "browser.enable_spellchecking","profile.exit_type","profile.exited_cleanly"]:
        p("    %-34s = %s" % (k, g(k)))

p("### 缓存可回收量汇总")
tot_clean = 0
for rel,tag in [("Default/Cache","浏览器缓存"),("Default/Code Cache","代码缓存"),
                ("Default/GPUCache","GPU缓存"),("ShaderCache","着色器缓存"),
                ("GrShaderCache","着色器缓存2"),("component_crx_cache","组件扩展缓存"),
                ("Default/Service Worker","Service Worker"),("Default/IndexedDB","站点数据库")]:
    fp = os.path.join(UD, rel.replace("/",os.sep))
    if os.path.isdir(fp):
        tt,ff = dsize(fp)
        p("  %-24s %8.1f MB" % (tag, mb(tt)))
        if tag in ("浏览器缓存","代码缓存","GPU缓存","着色器缓存","着色器缓存2"):
            tot_clean += tt
p("  --> 可直接清（浏览器内清）约 %.0f MB" % mb(tot_clean))
sys.stdout.write(base64.b64encode(B.getvalue().encode("utf-8")).decode())
