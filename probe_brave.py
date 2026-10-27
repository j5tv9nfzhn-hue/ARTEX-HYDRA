# -*- coding: utf-8 -*-
import base64, io, os, sys, json
B = io.StringIO()
def p(s=""): B.write(s + "\n")
def mb(n): return n / 1048576.0
def dsize(path):
    t = 0; f = 0
    for dp, dns, fns in os.walk(path):
        for fn in fns:
            try:
                t += os.path.getsize(os.path.join(dp, fn)); f += 1
            except OSError:
                pass
    return t, f

USER = os.environ.get("USERPROFILE", r"C:\Users\Administrator")
LA = os.path.join(USER, "AppData", "Local")
RA = os.path.join(USER, "AppData", "Roaming")
PF = r"C:\Program Files"
PF86 = r"C:\Program Files (x86)"

p("### 1. 安装目录探测")
cands = [
    os.path.join(PF, "BraveSoftware", "Brave-Browser", "Application"),
    os.path.join(PF86, "BraveSoftware", "Brave-Browser", "Application"),
    os.path.join(LA, "BraveSoftware", "Brave-Browser", "Application"),
    os.path.join(LA, "Programs", "Brave-Browser", "Application"),
]
found = None
for c in cands:
    exe = os.path.join(c, "brave.exe")
    if os.path.exists(exe):
        found = c
        p("  [OK] %s" % c)
        for fn in sorted(os.listdir(c)):
            fp = os.path.join(c, fn)
            if os.path.isfile(fp) and fn.lower().endswith((".exe", ".dll")):
                p("       %-40s %8.1f MB" % (fn, mb(os.path.getsize(fp))))
    else:
        p("  [--] %s" % c)

p("")
p("### 2. 用户数据目录（User Data）")
ud = os.path.join(LA, "BraveSoftware", "Brave-Browser", "User Data")
if not os.path.isdir(ud):
    p("  (未找到) %s" % ud)
else:
    p("  路径: %s" % ud)
    t, f = dsize(ud)
    p("  总占用: %.1f MB / %d 文件" % (mb(t), f))
    p("  --- 各子项（>5MB 才列） ---")
    rows = []
    for name in os.listdir(ud):
        fp = os.path.join(ud, name)
        if os.path.isdir(fp):
            tt, ff = dsize(fp)
            rows.append((tt, ff, name + "/"))
    rows.sort(reverse=True)
    for tt, ff, name in rows:
        if mb(tt) >= 5:
            p("    %-28s %9.1f MB %6d files" % (name, mb(tt), ff))
    p("  --- 其他顶层文件 ---")
    for name in sorted(os.listdir(ud)):
        fp = os.path.join(ud, name)
        if os.path.isfile(fp) and mb(os.path.getsize(fp)) >= 0.5:
            p("    %-28s %9.1f MB" % (name, mb(os.path.getsize(fp))))

p("")
p("### 3. 版本 + 配置概况")
# 版本：从 Application 目录的版本号文件夹推断
if found:
    vers = [d for d in os.listdir(found) if os.path.isdir(os.path.join(found, d)) and d[0].isdigit()]
    vers.sort()
    p("  程序版本目录: %s" % (", ".join(vers) if vers else "(无)"))
# Local State
ls = os.path.join(ud, "Local State")
if os.path.exists(ls):
    try:
        j = json.load(io.open(ls, encoding="utf-8", errors="replace"))
        p("  Local State: 可读, %d 键" % len(j))
    except Exception as e:
        p("  Local State: 解析失败 %s" % e)

# 配置文件统计
profiles = []
if os.path.isdir(ud):
    for name in os.listdir(ud):
        if name == "Default" or name.startswith("Profile "):
            profiles.append(name)
p("  配置文件(Profile)数量: %d  -> %s" % (len(profiles), ", ".join(profiles) if profiles else "无"))
for prof in profiles:
    pd = os.path.join(ud, prof)
    tt, ff = dsize(pd)
    p("    %-14s %8.1f MB %5d files" % (prof, mb(tt), ff))
    # 扩展
    extdir = os.path.join(pd, "Extensions")
    n_ext = 0
    if os.path.isdir(extdir):
        n_ext = len([d for d in os.listdir(extdir) if os.path.isdir(os.path.join(extdir, d))])
    p("      扩展数量: %d" % n_ext)
    # 偏好里的关键设置
    pref = os.path.join(pd, "Preferences")
    if os.path.exists(pref):
        try:
            j = json.load(io.open(pref, encoding="utf-8", errors="replace"))
            shields = j.get("brave", {}).get("shields", {})
            p("      Shields 关键值: %s" % json.dumps({
                "advanced_block": shields.get("advanced_blocking_enabled"),
                "fingerprinting": shields.get("fingerprinting_v2") or shields.get("fingerprinting_block"),
                "https_upgrade": shields.get("https_upgrade"),
                "cookies": shields.get("cookie_control"),
            }, ensure_ascii=False))
            pr = j.get("profile", {})
            p("      退出时清除: %s" % pr.get("exit_type"))
            brave = j.get("brave", {})
            p("      Rewards/Wallet/Stats: rewards=%s wallet=%s p3a=%s" % (
                brave.get("rewards", {}).get("enabled"),
                brave.get("wallet", {}).get("should_show_wallet"),
                brave.get("stats", {}).get("p3a")))
        except Exception as e:
            p("      Preferences 解析失败: %s" % e)
    # 各子目录明细
    rows = []
    for name in os.listdir(pd):
        fp = os.path.join(pd, name)
        if os.path.isdir(fp):
            tt2, ff2 = dsize(fp)
            rows.append((tt2, ff2, name))
    rows.sort(reverse=True)
    p("      最大子目录: " + ", ".join("%s %.1fMB" % (n, mb(t2)) for t2, f2, n in rows[:5]))

p("")
p("### 4. 缓存目录（可清理）")
for rel in ["BraveSoftware/Brave-Browser/User Data/Default/Cache",
            "BraveSoftware/Brave-Browser/User Data/Default/Code Cache",
            "BraveSoftware/Brave-Browser/User Data/Default/GPUCache",
            "BraveSoftware/Brave-Browser/User Data/ShaderCache",
            "BraveSoftware/Brave-Browser/User Data/GrShaderCache",
            "BraveSoftware/Brave-Browser/Crashpad"]:
    fp = os.path.join(LA, rel.replace("/", os.sep))
    if os.path.isdir(fp):
        tt, ff = dsize(fp)
        p("  %-58s %8.1f MB" % (rel, mb(tt)))

p("")
p("### 5. 对比：Chrome / Edge 占用")
for vendor, rel in [("Chrome", "Google/Chrome/User Data"), ("Edge", "Microsoft/Edge/User Data")]:
    fp = os.path.join(LA, rel.replace("/", os.sep))
    if os.path.isdir(fp):
        tt, ff = dsize(fp)
        p("  %-8s %8.1f MB %6d files  (%s)" % (vendor, mb(tt), ff, fp))
    else:
        p("  %-8s (未安装)" % vendor)

p("")
p("### 6. 系统信息")
try:
    import platform
    p("  OS: %s %s" % (platform.system(), platform.release()))
    p("  Python: %s" % platform.python_version())
except Exception:
    pass
try:
    p("  逻辑CPU: %s" % os.cpu_count())
except Exception:
    pass

sys.stdout.write(base64.b64encode(B.getvalue().encode("utf-8")).decode())
