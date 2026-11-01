package db

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// =====================================================================
// 企业主体层：网段（inet/cidr 语义）在 SQLite 上的实现
//
// 存储口径：SQLite 没有 inet/cidr 类型，company_scope.net 等列一律是 TEXT，
// 地址解析与网段判定全部下沉到本文件注册的 artex_* 标量函数里。
//
// 因此下列写法在本仓的 SQL 里都不应出现，出现了就是回归：
//
//	1. ::text / ::cidr / ::inet / ::jsonb 一类的类型转换
//	2. regexp_replace（改用 artex_regexp_replace）
//	3. ON CONFLICT ON CONSTRAINT <name>（按列名写冲突目标）
//	4. net >>= ip（改用 artex_inet_contains）
//	5. asset_ids @> ANY(...)（改用 artex_json_contains）
//	6. DISTINCT ON / UPDATE … FROM（用窗口函数改写）
//
// ---------------------------------------------------------------------
// 最要紧的一处：net >>= ip 到底在做什么
// ---------------------------------------------------------------------
//
// 它就是「网段包含」判定：语义等价于「把右边的地址按左边的网络做掩码后比前缀」，
// 也就是「这个 IP 是否落在这条网段规则里」。db/companies.go 有两处依赖它：
//
//	recomputeAttributionTx  重算归属时，网段规则按 masklen 从长到短择优
//	resolveCompanyWithICP   资产写入时同步解析归属
//
// 它不能被删掉——删掉之后「归属企业」对所有网段/IP 规则直接失效，
// TestCompanyScopeAttribution / TestResolveCompany / TestExplicitCompanyAttributionSurvivesScopeRebuild
// 都会失败。也不能靠 Go 侧预筛来顶替：重算语句要在一个事务里对整张 assets 表
// 做批量 UPDATE，把候选网段搬到 Go 侧会把归属判定拆成"读—判—写"三段。
//
// 判定顺序的逐条契约见 inetContains 的注释。
//
// ---------------------------------------------------------------------
// 为什么注册成函数而不是拼 SQL 片段
// ---------------------------------------------------------------------
//
// 与 db/dialect_arrays.go 的 artex_json_contains 同理：
//
//  1. 合法性判定必须复用 Go 的 net.ParseIP / net.ParseCIDR。写成
//     EXISTS(... json_each ...) / 字符串前缀比较之类的片段，等于把地址解析
//     在 SQLite 里重写一遍，而重写一遍就一定会和 Go 侧 ParseScopeLine 的
//     规范化分叉。
//  2. 非法输入必须返回"不匹配"而不是报错。assets.ip 是自由文本（Agent / 资产
//     API 可能写进主机名），一条脏数据不能把整条重算语句打挂。
//  3. 参数绑定：函数把值当普通实参收，调用点永远只写 $N。
//
// 函数名一律带 artex_ 前缀，与 dialect_arrays.go 的注册约定一致，也避免和
// 并行迁移任务注册的同名函数撞车（MustRegister* 对重名会 panic）。

// ---------------------------------------------------------------------
// 注册（init）
// ---------------------------------------------------------------------

func init() {
	// try_inet(value text) → inet
	sqlite.MustRegisterDeterministicScalarFunction("artex_try_inet", 1, artexTryInetFunc)
	// net >>= ip（网段包含）
	sqlite.MustRegisterDeterministicScalarFunction("artex_inet_contains", 2, artexInetContainsFunc)
	// masklen(net)
	sqlite.MustRegisterDeterministicScalarFunction("artex_inet_masklen", 1, artexInetMasklenFunc)
	// lower(regexp_replace(x, '[[:space:]]+', '', 'g'))
	sqlite.MustRegisterDeterministicScalarFunction("artex_icp_key", 1, artexICPKeyFunc)
	// jsonb_array_length(x)
	sqlite.MustRegisterDeterministicScalarFunction("artex_json_array_length", 1, artexJSONArrayLengthFunc)
	// asset_ids @> ANY($n::jsonb[])
	sqlite.MustRegisterDeterministicScalarFunction("artex_json_contains_any", 2, artexJSONContainsAnyFunc)
}

// ---------------------------------------------------------------------
// 文本参数
// ---------------------------------------------------------------------

// dialectText 把绑定参数/列值读成文本。第二个返回值区分「NULL」与「空串」，
// 因为 artex_try_inet 是 STRICT 的：NULL 进、NULL 出，不能变成空串。
//
// 与 sqlite_compat.go 的 argString 的差别：int64 不会被静默吞成空串。资产 id
// 之类的整型即使误传进来，这里也会给出它的十进制文本，随后由解析函数判非法。
func dialectText(v driver.Value) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case string:
		return t, true
	case []byte:
		return string(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	default:
		return fmt.Sprint(v), true
	}
}

// ---------------------------------------------------------------------
// inet / cidr 解析
// ---------------------------------------------------------------------

// parseInetText 解析一个「inet」形态的文本，返回地址与掩码位数。
//
// 契约：
//   - '10.0.0.1'         → 地址 10.0.0.1，掩码 32 位（不带掩码时按满位算，
//     与 artex_inet_masklen 的口径一致）；
//   - '2001:db8::1'      → 掩码 128 位；
//   - '10.1.2.3/24'      → 保留主机位，掩码 24 位（inet 语义，不像 cidr 会清零）；
//   - 'notanip'          → ok=false，由调用方转成 NULL（脏数据不报错）。
func parseInetText(raw string) (net.IP, int, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, 0, false
	}
	if strings.ContainsRune(s, '/') {
		ip, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return nil, 0, false
		}
		ones, bits := ipnet.Mask.Size()
		if bits == 0 {
			return nil, 0, false
		}
		return normalizeIP(ip), ones, true
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, 0, false
	}
	v4 := ip.To4()
	if v4 != nil {
		return v4, 32, true
	}
	return ip.To16(), 128, true
}

// parseScopeNetText 解析 company_scope.net 一列。它是 TEXT 列，内容由
// ParseScopeLine 产出（要么 ipnet.String()，要么单 IP 加 /32、/128），因此一定
// 带掩码；这里对不带掩码的输入按满位兜底，与 inet 的缺省行为保持一致。
func parseScopeNetText(raw string) (*net.IPNet, bool) {
	s := strings.TrimSpace(raw)
	if strings.ContainsRune(s, '/') {
		_, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return nil, false
		}
		return ipnet, true
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, false
	}
	if v4 := ip.To4(); v4 != nil {
		return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}, true
	}
	return &net.IPNet{IP: ip.To16(), Mask: net.CIDRMask(128, 128)}, true
}

// normalizeIP 把地址统一成与网络同宽的字节形式：IPv4（含 ::ffff: 开头的
// v4-mapped 形式）取 4 字节，其余取 16 字节：v4-mapped 的 IPv6 一律当 IPv4
// 处理，否则「IPv4 网段 vs 同一地址的 IPv6 写法」会被判成地址族不同而失配。
func normalizeIP(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip.To16()
}

// inetContains 是 artex_inet_contains(net, ip) 的实现，即「网段包含」判定。
// 判定顺序逐条固定如下（改动前先读，每条都有对应的历史回归）：
//
//  1. 左操作数解析不出网段 → false。net 是自由文本，脏值按"不匹配"处理
//     而不是报错。
//  2. 右操作数解析不出地址 → false。assets.ip 是自由文本，一条
//     "api.example.com" 不能打挂整条重算语句。
//  3. 地址族不同（IPv4 网段 vs IPv6 地址，或反之）→ false。
//  4. 左操作数掩码位数 > 右操作数掩码位数 → false。右边若是 '10.0.0.0/8'
//     这种带主机位的地址，只比前 8 位，所以比 /24 的网段还不匹配；
//     Go 的 IPNet.Contains 在这里会判 true，因此必须显式挡掉，不能直接用 Contains。
//  5. 否则把两边都按网段掩码清零后逐位比较 —— 这就是普通的「IP 落在网段内」。
//
// 返回值永远是 true/false，不返回 error：整个重算流程必须对脏数据免疫。
func inetContains(netText, ipText string) bool {
	ipnet, ok := parseScopeNetText(netText)
	if !ok {
		return false
	}
	ip, ipBits, ok := parseInetText(ipText)
	if !ok {
		return false
	}
	netIP := normalizeIP(ipnet.IP)
	ones, bits := ipnet.Mask.Size()
	if bits == 0 || len(netIP) != bits/8 {
		return false
	}
	if len(ip) != len(netIP) {
		return false // 地址族不同（IPv4 网段 vs IPv6 地址，或反之）
	}
	if ones > ipBits {
		return false
	}
	mask := ipnet.Mask
	return netIP.Mask(mask).Equal(ip.Mask(mask))
}

// ---------------------------------------------------------------------
// SQLite 侧标量函数实现
// ---------------------------------------------------------------------

// artexTryInetFunc 实现 artex_try_inet(value text)。
//
// 契约（STRICT + 容错）：
//   - NULL 进 NULL 出；
//   - 非法文本返回 NULL，而不是报错——脏数据不能打挂整条语句。
//
// 返回值是规范化后的地址文本（本项目只拿它去做网段包含判定和判 IS NULL，文本本身
// 不进业务逻辑）：无掩码时给纯地址，有掩码时给 "addr/bits"。
func artexTryInetFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) < 1 {
		return nil, nil
	}
	s, ok := dialectText(args[0])
	if !ok {
		return nil, nil // STRICT
	}
	ip, bits, ok := parseInetText(s)
	if !ok {
		return nil, nil // EXCEPTION WHEN others THEN RETURN NULL
	}
	if strings.ContainsRune(strings.TrimSpace(s), '/') {
		return fmt.Sprintf("%s/%d", ip.String(), bits), nil
	}
	return ip.String(), nil
}

// artexInetContainsFunc 实现 artex_inet_contains(net, ip) → 0/1。
// 语义见 inetContains。
func artexInetContainsFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) < 2 {
		return int64(0), nil
	}
	netText, ok := dialectText(args[0])
	if !ok {
		return int64(0), nil // net IS NULL → NULL >>= x 是 NULL，连接条件不成立
	}
	ipText, ok := dialectText(args[1])
	if !ok {
		return int64(0), nil
	}
	if inetContains(netText, ipText) {
		return int64(1), nil
	}
	return int64(0), nil
}

// artexInetMasklenFunc 实现 artex_inet_masklen(net) → 前缀位数 / NULL。
// 即「前缀位数」，用于「更长（更窄）的网段优先」那条 ORDER BY。
// 解析不出网段时给 NULL，NULL 进 NULL 出。
func artexInetMasklenFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) < 1 {
		return nil, nil
	}
	s, ok := dialectText(args[0])
	if !ok {
		return nil, nil
	}
	ipnet, ok := parseScopeNetText(s)
	if !ok {
		return nil, nil
	}
	ones, bits := ipnet.Mask.Size()
	if bits == 0 {
		return nil, nil
	}
	return int64(ones), nil
}

// artexICPKeyFunc 实现 artex_icp_key(x)。
//
// 即「去掉全部空白后转小写」的归一化键。
//
// SQLite 没有 regexp_replace。与其把 POSIX 字符类翻译成一段 SQL 片段再去猜
// 它和 Go 侧的正则行为差在哪，不如直接复用 db/company_scope.go 的 NormalizeICP：
// 规则值 cs.value 本来就是 NormalizeICP 写进去的（ParseScopeInput → icp 分支），
// 两边用同一个函数算 key 才能保证「存的」和「比的」严格同形。
//
// NormalizeICP 去的是全部 Unicode 空白字符（含 U+00A0、U+3000 这类
// 非 ASCII 空白），范围严格大于 POSIX 的 [[:space:]]，只会让匹配更宽松，
// 不会把原本该匹配的判成不匹配。
func artexICPKeyFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) < 1 {
		return nil, nil
	}
	s, ok := dialectText(args[0])
	if !ok {
		return nil, nil
	}
	return NormalizeICP(s), nil
}

// artexJSONArrayLengthFunc 实现 artex_json_array_length(x)。
//
// 走 jsonArrayOf 归一化，因此 NULL、空串、'{}'（数组列的 schema 默认值）、
// 数组字面量 '{5}' 全部退化成 0，而不是像 SQLite 内建的 json_array_length
// 那样对 '{}' 返回 0、对损坏文本直接报 malformed JSON。
func artexJSONArrayLengthFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) < 1 {
		return int64(0), nil
	}
	return int64(len(jsonArrayOf(argString(args[0])))), nil
}

// artexJSONContainsAnyFunc 实现 artex_json_contains_any(col, idsJSON)。
//
// 右操作数 $n 是 assetIDContainments 产出的 ["[1]","[2]",…] 数组：包含判定是
// "元素集合相交"，'[1]' 等价于「1 是 col 的元素之一」，再摊成"存在任一 id 命中"。
//
// 右操作数是一段 JSON 数组文本（Go 侧用 jsonArrayText
// 产出，永远合法），再逐元素比 jsonScalarKey——与 dialect_arrays.go 的
// artex_json_contains 用的是同一把尺子，因此数字 5 不会和字符串 "5" 互相误命中。
func artexJSONContainsAnyFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) < 2 {
		return int64(0), nil
	}
	got := jsonArrayOf(argString(args[0]))
	if len(got) == 0 {
		return int64(0), nil
	}
	for _, want := range jsonArrayOf(argString(args[1])) {
		key := jsonScalarKey(want)
		for _, el := range got {
			if jsonScalarKey(el) == key {
				return int64(1), nil
			}
		}
	}
	return int64(0), nil
}

// ---------------------------------------------------------------------
// SQL 片段助手
// ---------------------------------------------------------------------

// companyInetContains 表达「网段包含地址」判定。
//
//	artex_inet_contains(net, $1)
//
// 两个操作数都必须是「值」的表达式（列或 $N 占位符），实现见 inetContains。
func companyInetContains(netExpr, addrExpr string) string {
	return "artex_inet_contains(" + netExpr + ", " + addrExpr + ")"
}

// companyInetMasklen 表达网段的前缀位数。
//
//	artex_inet_masklen(net)
//
// 只用于「更长（更窄）的网段优先」那条 ORDER BY：对 IPv4 返回 0..32、对 IPv6
// 返回 0..128，两族可以同序比较。
func companyInetMasklen(netExpr string) string {
	return "artex_inet_masklen(" + netExpr + ")"
}

// companyTryInet 表达「安全地把文本解析成地址，解析不出给 NULL」。
//
//	artex_try_inet(a.ip)
//
// 用在两个地方：网段归属的「安全转地址」，以及 malformedIPAssetWarning 里
// 「哪些资产的 ip 解析不出来」那条告警查询。
func companyTryInet(valueExpr string) string {
	return "artex_try_inet(" + valueExpr + ")"
}

// companyICPKey 表达 ICP 归属用的规范化 key。
//
//	artex_icp_key(x)
//
// 语义见 artexICPKeyFunc：算成「去空白 + 小写」，与写入 cs.value 的
// NormalizeICP 同形。
func companyICPKey(valueExpr string) string {
	return "artex_icp_key(" + valueExpr + ")"
}

// companyJSONArrayLength 表达「这个 JSON 数组列里有几个元素」。
//
//	artex_json_array_length(COALESCE(f.asset_ids, '[]'))
//
// '[]' 不带类型标注：COALESCE 是逐值的通用表达式、没有类型标注位置，而 '[]'
// 本身就是一个普通文本字面量。
// NULL / 空串 / '{}' 这三种历史形状由 artex_json_array_length 内部统一归一成
// 长度 0，所以调用点不需要再包一层 CASE。
func companyJSONArrayLength(col string) string {
	return "artex_json_array_length(COALESCE(" + col + ", '[]'))"
}

// companyCidrArg 表达写入 company_scope.net 时的取值。
//
//	$3
//
// net 是 TEXT 列，值由 ParseScopeLine 产出（已经是规范化 CIDR 文本），不需要
// 也不应该有类型转换——`::$3::cidr` 这类标注会被 SQLite 报
// unrecognized token ":"。
func companyCidrArg(ph string) string {
	return ph
}

// companyInetAddrArg 表达「把绑定参数当成一个 inet 值」的转换，用作
// companyInetContains 的右操作数。
//
//	$1
//
// net 是 TEXT 列，artex_inet_contains 自己就做文本解析，不需要（也不能）有
// 类型转换——裸的 `$1::inet` 会被 SQLite 报 unrecognized token ":"。
func companyInetAddrArg(ph string) string {
	return ph
}

// companyScopeConflictTarget 表达 company_scope 插入语句的冲突目标。
//
//	ON CONFLICT (company_id, domain)
//
// 冲突目标按列名写，不写约束名（SQLite 不支持 ON CONFLICT ON CONSTRAINT）。
// 列清单的依据（逐字对照）：
//
//	db/schema_sqlite.sql:125        CONSTRAINT uq_sv2_domain UNIQUE (company_id, domain)
//	db/schema_sqlite.sql:163  CONSTRAINT uq_sv2_domain UNIQUE (company_id, domain)
//	db/schema_sqlite.sql:126        CONSTRAINT uq_sv2_net    UNIQUE (company_id, net)
//	db/schema_sqlite.sql:164  CONSTRAINT uq_sv2_net    UNIQUE (company_id, net)
//
// SQLite 不支持按约束名指定冲突目标，但表级 UNIQUE 约束同样会建出一个唯一
// 索引（sqlite_autoindex_*），`ON CONFLICT (列…)` 正是按这个索引做仲裁。
// 两个约束都是复合的，所以必须给完整列清单，只写一列会匹配不上任何唯一索引，
// 直接报 "ON CONFLICT clause does not match any PRIMARY KEY or UNIQUE constraint"。
//
// **不会重复插行**：ON CONFLICT (company_id, domain) 与 ON CONFLICT (company_id, net)
// 各自都命中上面对应的那个 UNIQUE 索引，重复规则仍然被 DO NOTHING 吞掉。
func companyScopeConflictTarget(constraint string, cols ...string) string {
	return "ON CONFLICT (" + strings.Join(cols, ", ") + ")"
}

// findingAssetScopeIDsCond 表达「findings.asset_ids 与这批资产 id 有交集」。
//
//	artex_json_contains_any(f.asset_ids, $2) —— 参数是一段 JSON 数组文本
//
// 返回值里已经带好 $N 编号，并把对应参数追加进 args。
//
// 参数为什么是 JSON 文本而不是切片：SQLite 驱动无法把 []string / []int64 当成
// 一个参数绑定（只会得到 "unsupported type []string"）。与 dialect_arrays.go 的
// appendInPlaceholders 同源，这里选择把集合在 Go 侧编码成一段 JSON 文本再交给
// UDF——值仍然是绑定进去的，SQL 文本里没有任何用户输入。
func findingAssetScopeIDsCond(colExpr string, ids []int64, args *[]any) string {
	*args = append(*args, companyJSONIDSet(ids))
	return fmt.Sprintf("artex_json_contains_any(%s, $%d)", colExpr, len(*args))
}

// companyJSONIDSet 把资产 id 列表编码成 SQLite 侧的 JSON 数组文本，
// 供 artex_json_contains_any 使用。空集合编码成 "[]"，与 findings.asset_ids
// 的列默认值同一形状。
func companyJSONIDSet(ids []int64) string {
	vals := make([]any, 0, len(ids))
	for _, id := range ids {
		vals = append(vals, id)
	}
	return jsonArrayText(vals)
}

// findingAssetRowsGoneCond 表达「这条发现关联的资产已经全没了」。
//
//	json_each(artex_json_array(f.asset_ids)) 与 assets 联表
//
// 语义：asset_ids 为空（长度 0），或者里面列出的 id 在 assets 表里一个都查不到
// （资产已被删除）。两类都要算进「未关联资产」桶，否则桶上的计数会大于点开后
// 能查到的条数。
//
// 先把 f.asset_ids 过一遍 artex_json_array 归一化再交给 json_each：内建
// json_each 遇到损坏文本会直接报 malformed JSON，而 findings 历史上可能存在
// '{}'（空数组字面量）这类不是合法 JSON 数组的值。归一化之后传给 json_each 的
// 永远是合法 JSON 数组（至少是 '[]'），脏数据只会得到"0 行"而不是让整条筛选
// 语句失败。
//
// 元素侧的 typeof(e.value) = 'integer' 是刻意加的：CAST('abc' AS INTEGER) 会
// 静默变成 0，用 typeof 过滤等于声明"非整数元素不参与匹配"，与 asset_ids 永远
// 只存整数的事实一致。
func findingAssetRowsGoneCond(jsonExpr string) string {
	return `NOT EXISTS (
			SELECT 1 FROM json_each(artex_json_array(` + jsonExpr + `)) e
			WHERE typeof(e.value) = 'integer'
			  AND EXISTS (SELECT 1 FROM assets a WHERE a.id = e.value)
		)`
}

// findingNodePayloadSetExpr 表达「把 payload.<jsonKey> 设成字符串 $1」。
//
//	json_set(payload, '$.<key>', json_quote($1))
//
// 两处必须照做：
//   - 路径语法：json_set 要求 '$.key' 而不是 '{key}'；
//   - 值必须强制成 JSON 字符串：json_set 会把"看起来像合法 JSON"的值原样嵌
//     进去（json_set('{}','$.a','123') 得到数字而不是字符串），所以外面要套
//     一层 json_quote。
//   - jsonKey 必须是可信常量（severity / name / vulnclass），与调用点
//     setFindingCol 的既有约定一致，绝不接受用户输入。
func findingNodePayloadSetExpr(jsonKey string) string {
	return "json_set(payload, '$." + jsonKey + "', json_quote($1))"
}

// ---------------------------------------------------------------------
// 时间戳落点
// ---------------------------------------------------------------------

// findingTimestampScan 承接 ListFindingGroups 里 MAX(f.created_at) 的落点。
//
// 差异不在 SQL 而在驱动：SQLite 驱动只有当 sqlite3_column_decltype 返回
// DATE/DATETIME/TIMESTAMP 时才把 TEXT 解析成 time.Time，而 MAX(...) 是表达式，
// decltype 为空，于是交出来就是一段和列里一模一样的文本。
//
// 改 SQL 没有等价写法（换不成保持 decltype 的写法），因此在 Scan 落点上分叉——
// 读到的文本与列里存的是同一串字符，按驱动自己的时间布局解析回去就是同一个时刻。
type findingTimestampScan struct {
	Time time.Time
	Text sql.NullString
}

// dest 返回应当交给 rows.Scan 的落点。
func (s *findingTimestampScan) dest() any {
	return &s.Text
}

// value 解析出最终的时间。SQL 值只可能是两种：驱动交出来的 time.Time，或
// NULL（分组内没有任何 finding 时 MAX 为 NULL）；解析不出来按错误报出，
// 不静默塞一个零值——那会把"时间戳坏了"伪装成"时间戳是零值"。
func (s *findingTimestampScan) value() (time.Time, error) {
	if !s.Text.Valid {
		return time.Time{}, nil
	}
	return parseSQLiteTimestamp(s.Text.String)
}

// parseSQLiteTimestampLayouts 与 modernc.org/sqlite 的 parseTimeFormats 保持一致
// （sqlite.go 的日期时间语法 1–7），少一条就会把某种形状误判成"坏时间戳"。
var parseSQLiteTimestampLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

// parseSQLiteTimestamp 解析 SQLite 侧读出的时间戳文本。首条命中的布局就是写入
// 路径用的那一条：列默认值是 strftime('%Y-%m-%d %H:%M:%f+00:00','now')，
// now() UDF 是 2006-01-02 15:04:05.000-07:00，两者都落在第一条布局上。
func parseSQLiteTimestamp(s string) (time.Time, error) {
	for _, layout := range parseSQLiteTimestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析 SQLite 时间戳 %q", s)
}
