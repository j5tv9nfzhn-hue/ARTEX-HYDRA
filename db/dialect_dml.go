package db

import (
	"database/sql/driver"
	"net"
	"strings"
	"unicode"

	"modernc.org/sqlite"
)

// =====================================================================
// 写语句与范围匹配谓词
// =====================================================================
//
// 文件名沿用 dialect_* 的叫法，和 db/dialect_mixed.go（锁与类型转换）、
// db/dialect_arrays.go（数组列）、db/dialect_dsl.go（查询 DSL 谓词）、
// db/dialect_softdelete.go（xmax 惯用法）并列。分工边界：
//
//	dialect_mixed.go     行锁子句、ILIKE、::text、now()+interval
//	dialect_arrays.go    数组列的读写与 ANY/unnest/array_append
//	dialect_dsl.go       查询 DSL 按引擎拼出来的谓词
//	dialect_softdelete.go xmax 惯用法
//	dialect_dml.go       ← 本文件：写语句 + 范围匹配谓词
//
// ---------------------------------------------------------------------
// 本文件收口两件事
// ---------------------------------------------------------------------
//
// A. **CTE 内部 DML**（db/task_categories.go）
//
//	    WITH inserted AS (INSERT ... RETURNING *) SELECT ... FROM inserted
//
//	SQLite 只把 SELECT 放进 CTE：CTE 的表值主体解析出来必须是一个 SELECT，
//	INSERT / UPDATE 一进去就报 `near "INSERT": syntax error`。
//
//	因此改用**顶层** `INSERT/UPDATE ... RETURNING`：
//	  - RETURNING 自 3.35.0 起就是语句的一部分，不依赖 CTE；
//	  - `ON CONFLICT DO NOTHING` 命中冲突时不写任何行，RETURNING 就返回 0 行；
//	  - RETURNING 的输出表达式可以是任意 SQLite 表达式，包括常量
//	    （原外层 SELECT 里的 `0`）和标量子查询（原 CTE 里算 task_count 的
//	    `(SELECT count(*) ...)`）。
//	这几条都用本机 modernc.org/sqlite v1.52.0 实测过，因此语句可以写成一条，
//	不需要分支；"插入还是冲突"一律交给 Go 侧按返回行数（sql.ErrNoRows）判断。
//
//	B. **范围匹配谓词**（db/task_scope.go 的 covTargetCTE）
//
//	    ts.net >>= try_inet(a.ip)
//	    lower(regexp_replace(COALESCE(a.icp,''), '[[:space:]]+', '', 'g')) = ts.value
//
//	`>>=` 是"是否被包含"运算符，`try_inet()` 与 `regexp_replace()` 也是函数。
//	SQLite 三样都没有，而且 `>>=` 是**中缀运算符**，SQLite 不提供运算符重载机制，
//	任何标量函数都顶不上。只能把整个谓词搬进 Go：注册 artex_scope_* 标量函数，
//	拼成 `artex_scope_net_contains(ts.net, a.ip)` 这种函数调用。
//
//	函数调用在这里比"拼 EXISTS (SELECT 1 FROM json_each(...))"片段更合适：
//	值只是普通实参，调用处永远只写列名，没有任何把用户值拼进 SQL 的机会。
//
//	函数名统一带 `artex_scope_` 前缀而不是直接注册 `try_inet` / `regexp_replace`
//	这些名字：任务范围谓词在 db/task_assets_context.go 里还有另外两份同样写法
//	（contextCoverageCTE / scopeTargetCTE），用中性前缀注册可以避免命名撞车。
//
// ---------------------------------------------------------------------
// 已知残留（不在本文件的责任范围）
// ---------------------------------------------------------------------
// db/task_assets_context.go 里的 contextCoverageCTE / scopeTargetCTE 也写着
// `ts.net >>= try_inet(...)`，但那个文件不在本次改动范围内，因此
// BuildCoverageGraph / TaskCoverageWithSources / HostsByTaskWithSources 在
// SQLite 上仍会失败。修法与本文件一致：把该谓词也换成
// artex_scope_net_contains(...)。

// ---------------------------------------------------------------------
// 注册（init）
// ---------------------------------------------------------------------

// 注册时机与 db/dialect_arrays.go、db/sqlite_compat.go 一致：必须在第一次
// sql.Open 之前，因此放在 init() 里。
//
// 这里刻意用 RegisterDeterministicScalarFunction（返回 error）而不是
// MustRegisterDeterministicScalarFunction（重复注册直接 panic）：本包正被
// 多个并行迁移任务同时改动，别人若先注册了同名函数，panic 会把整个 db 包的
// 测试全部带崩。重复注册只说明"已经有人注册过同名实现"，忽略即可。
func init() {
	_ = sqlite.RegisterDeterministicScalarFunction("artex_scope_net_contains", 2, scopeNetContainsFunc)
	_ = sqlite.RegisterDeterministicScalarFunction("artex_scope_icp_key", 1, scopeICPKeyFunc)
}

// ---------------------------------------------------------------------
// A. task_categories 的写语句
// ---------------------------------------------------------------------

// taskCategoryInsertQuery 返回 CreateTaskCategory 的语句。
//
// 两个引擎共用这一条：原语句的
// `WITH inserted AS (INSERT ... RETURNING *) SELECT ... FROM inserted` 里，
// 外层 SELECT 只是把 RETURNING 出来的行**原样再投影一遍**（外层唯一的"计算"
// 是常量 `0`，即 task_count 在新建时恒为 0），没有任何过滤、聚合或关联。改成
// 顶层 `INSERT ... RETURNING id, name, nkey, 0, created_at, updated_at` 后列序
// 与 scanTaskCategory 完全一致，返回的还是同一行。
//
// 冲突判定：`ON CONFLICT (nkey) DO NOTHING` 在冲突时"一条都没写"，于是 RETURNING
// 返回 0 行 → database/sql 交回
// sql.ErrNoRows → 调用方映射成 ErrTaskCategoryNameConflict。语义一致。
const taskCategoryInsertQuery = `
INSERT INTO task_categories(name, nkey)
VALUES ($1,$2)
ON CONFLICT (nkey) DO NOTHING
RETURNING id, name, nkey, 0, created_at, updated_at`

// taskCategoryRenameQuery 返回 RenameTaskCategory 的语句。
//
// 同上：原来的
// `WITH updated AS (UPDATE ... RETURNING *) SELECT ...` 外层 SELECT 唯一多做的
// 事是算 task_count，而 SQLite 的 RETURNING 里可以直接写标量子查询（已实测），
// 所以整条塌回一条顶层 UPDATE：
//
//	改写前：WITH updated AS (UPDATE ... RETURNING *)
//	             SELECT updated.id, ..., (SELECT count(*) FROM tasks
//	                                      WHERE category_id=updated.id AND deleted_at IS NULL), ...
//	现在：  UPDATE ... RETURNING id, name, nkey,
//	                       (SELECT count(*) FROM tasks
//	                        WHERE category_id=task_categories.id AND deleted_at IS NULL), ...
//
// 两条里的表名引用指向同一张被更新的表（相关子查询），过滤条件
// `deleted_at IS NULL` 逐字照搬。0 行 = 分类不存在 → ErrTaskCategoryNotFound。
//
// 附带说明：SQLite 上 RETURNING 的 updated_at 是**触发器改写之前**的值（RETURNING
// 不反映触发器的改动）。这个字段是时间戳、没有任何调用点拿它做判定，属于已知的
// 可接受差异。
const taskCategoryRenameQuery = `
UPDATE task_categories SET name=$2, nkey=$3 WHERE id=$1
RETURNING id, name, nkey,
       (SELECT count(*) FROM tasks WHERE category_id=task_categories.id AND deleted_at IS NULL),
       created_at, updated_at`

// taskCategoryAssignSQLiteUpdate 是上面那条 CTE 语句在 SQLite 上的第 1 步：
// 挂分类。`EXISTS(SELECT 1 FROM task_categories WHERE id=$2)` 就是原
// `EXISTS (SELECT 1 FROM selected)` 的内联形式（SQLite 的 CTE 里不能放 DML，
// 但普通子查询可以），所以"分类不存在"和"任务不存在"两种情况都会在这里
// 收敛成 0 行，由 Go 侧区分。
const taskCategoryAssignSQLiteUpdate = `
UPDATE tasks SET category_id=$2
WHERE id=$1 AND deleted_at IS NULL AND EXISTS (SELECT 1 FROM task_categories WHERE id=$2)
RETURNING id`

// taskCategoryAssignSQLiteSelect 是第 2 步：拿改完之后的分类行。
// 与 taskCategoryCols 同一套投影（LEFT JOIN + count FILTER），因此返回的
// task_count 已经是本次移动之后的值，与改写前外层 SELECT 里的标量子查询读到的
// 口径相同。第 1 步是写语句、事务一开就拿到 SQLite 的写锁
// （DSN 的 _txlock=immediate），两步之间不会有别的连接插进来。
const taskCategoryAssignSQLiteSelect = `
SELECT ` + taskCategoryCols + `
FROM task_categories category
LEFT JOIN tasks task ON task.category_id=category.id
WHERE category.id=$1
GROUP BY category.id`
const taskCategoryBatchMoveSQLiteHead = `
UPDATE tasks SET category_id=$1
WHERE id IN (`

const taskCategoryBatchMoveSQLiteTail = `) AND deleted_at IS NULL
RETURNING id`

// taskCategoryBatchMoveStmt 组装批量改分类的语句与参数。
//
// 绑定顺序：$1=categoryID，任务 id 从 $2 开始顺序展开。
func taskCategoryBatchMoveStmt(args *[]any, taskIDs []int64, categoryID *int64) string {
	*args = append(*args, categoryID)
	return taskCategoryBatchMoveSQLiteHead + appendInPlaceholders(args, taskIDs) + taskCategoryBatchMoveSQLiteTail
}

// covTargetCTESQLite 是「任务范围 → 资产」的匹配 CTE。
//
// 【P4】它原本还有一份逐行相同、只有两处表达式不同的副本（covTargetCTEPG）。
// ARTEX 只用 SQLite，那份没有调用方的副本已经删掉——保留一个永远不执行的 SQL
// 常量，只会让后来的人误以为存在第二条代码路径。
//
// 两处表达式的对应关系，记录在这里而不是留在代码里：
//
//	ts.net >>= try_inet(a.ip)                    （cidr 是否包含该 inet）
//	    → artex_scope_net_contains(ts.net, a.ip)
//	lower(regexp_replace(x,'[[:space:]]+','','g')) （去空白 + 折叠大小写）
//	    → artex_scope_icp_key(x)
//
// 两个函数都是自定义 SQL 函数（见 scopeNetContains / scopeICPKey 的注释与
// 注册处），语义包括"值为 NULL 时不命中"这一条。
const covTargetCTESQLite = `
target AS (
  SELECT DISTINCT a.id, a.type,
         COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, '') AS label
  FROM assets a
  JOIN task_scope ts ON ts.task_id = $1 AND (
       (ts.kind='company'     AND a.company_id = ts.company_id)
    OR (ts.kind='root_domain' AND a.root_domain = ts.domain)
    OR (ts.kind='subdomain'   AND a.domain = ts.domain)
    OR (ts.kind IN ('ip','cidr') AND artex_scope_net_contains(ts.net, a.ip))
    OR (ts.kind='icp' AND (
         artex_scope_icp_key(COALESCE(a.icp,'')) = ts.value
         OR artex_scope_icp_key(COALESCE(a.app_icp,'')) = ts.value
       ))
  )
),
tested AS (
  SELECT DISTINCT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id = ea.node_id
  WHERE en.exploration_id = $2 AND en.kind = 'fact'
)`

// covTargetCTE 返回这段 CTE 在当前引擎上的写法。
func covTargetCTE() string {
	return covTargetCTESQLite
}

// ---------------------------------------------------------------------
// B 的 SQLite 实现
// ---------------------------------------------------------------------

// scopeNetContainsFunc 实现任务范围的「网段包住这个 IP 吗」谓词。
//
// 两个入参都是纯文本（schema 里 task_scope.net 与 assets.ip 都是 TEXT），没有
// NULL 以外的特殊形态；解析不了就返回 0（不命中），等价于"解析失败 → 比较结果为
// NULL → WHERE 不命中"。
func scopeNetContainsFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) < 2 {
		return int64(0), nil
	}
	if scopeNetContains(argString(args[0]), argString(args[1])) {
		return int64(1), nil
	}
	return int64(0), nil
}

// scopeNetContains 判断 addr 是否落在 cidrText 指定的网段里。
//
// 三条语义约定：
//   - 地址族不同（v4 网段 vs v6 地址）时返回 false 而不是报错；
//   - 网段按"主机位已清零"比较（Go 侧存进去的 '10.0.188.136/32' 读出来仍是
//     '10.0.188.136/32'），这里直接用 ParseCIDR 返回的 *IPNet.IP（已按掩码
//     对齐）作比较基准；
//   - 裸 IP（没有 "/"，Go 侧只在个别老数据里可能出现）按 /32 或 /128 处理。
func scopeNetContains(cidrText, addrText string) bool {
	mask, bits, base, ok := parseScopeCIDR(cidrText)
	if !ok {
		return false
	}
	addr := net.ParseIP(strings.TrimSpace(addrText))
	if addr == nil {
		return false
	}
	if v4 := addr.To4(); v4 != nil {
		addr = v4
	} else {
		addr = addr.To16()
	}
	if addr == nil || len(addr)*8 != bits {
		return false // 地址族不一致（v4 网段 vs v6 地址）
	}
	return addr.Mask(mask).Equal(base)
}

// parseScopeCIDR 把 "10.0.0.0/8" / "10.0.188.136/32" / 裸 "10.0.188.136" 统一
// 解析成 (掩码, 位宽, 已按掩码对齐的网络地址)。位宽 32 表示 IPv4、128 表示
// IPv6，后面用它做地址族一致性判定。
func parseScopeCIDR(s string) (net.IPMask, int, net.IP, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, 0, nil, false
	}
	if _, ipNet, err := net.ParseCIDR(s); err == nil {
		ones, bits := ipNet.Mask.Size()
		if ones == 0 && bits == 0 {
			return nil, 0, nil, false // 非连续掩码
		}
		return ipNet.Mask, bits, ipNet.IP, true
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, 0, nil, false
	}
	if v4 := ip.To4(); v4 != nil {
		return net.CIDRMask(32, 32), 32, v4, true
	}
	return net.CIDRMask(128, 128), 128, ip.To16(), true
}

// scopeICPKeyFunc 实现 `lower(regexp_replace(x, '[[:space:]]+', ”, 'g'))`。
func scopeICPKeyFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) < 1 {
		return "", nil
	}
	return scopeICPKey(argString(args[0])), nil
}

// scopeICPKey 先删掉所有空白字符再折叠大小写。
//
// `'[[:space:]]+'` 配 'g' 标志就是"把每一处连续空白整段删掉"（不是替换成单个
// 空格），所以实现是纯过滤而不是替换。空白判定用 unicode.IsSpace，它覆盖
// ASCII 的 space/\t/\n/\v/\f/\r（与 POSIX 的 [[:space:]] 一致）外加 NBSP、
// U+3000 等 Unicode 空白；ICP 备案值里出现这些的概率极低，多认几种空白不会改变
// 业务判定。
//
// 大小写折叠用 strings.ToLower 而不是 SQLite 的 lower()：后者只处理 ASCII。
func scopeICPKey(s string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s))
}
