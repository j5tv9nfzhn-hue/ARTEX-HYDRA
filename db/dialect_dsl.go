package db

import (
	"fmt"
)

// 资产查询 DSL 用到的 JSON 数组列谓词构造。
//
// 这些表达式属于资产查询 DSL（db/asset_dsl.go）——DSL 把用户输入编译成 SQL，
// 所以谓词是拼出来的，而不是写死在查询文本里。

// jsonArrayILikeExpr 构造「数组中任一元素模糊匹配」的谓词。
//
//	EXISTS (SELECT 1 FROM json_each(col) WHERE value LIKE $N)
//
// 用 LIKE 而不是显式做大小写折叠：SQLite 的 LIKE 对 ASCII 本身就大小写不敏感。
// 这一点与 assets.go 里其余 ILIKE 列的处理保持一致，不引入新的行为差异。
func jsonArrayILikeExpr(col, placeholder string) string {
	return "EXISTS (SELECT 1 FROM json_each(" + col + ") WHERE value LIKE " + placeholder + ")"
}

// assetIDsAnyExpr 构造「该行属于给定 id 集合」的谓词。
//
//	id IN ($1,$2,...)
//
// 集合成员在 Go 侧展开成 IN 列表而不是绑定一个数组参数：驱动无法把 []int64
// 绑成单参（"unsupported type []int64"）。展开为占位符后每个值仍各自绑定，
// 不存在拼接字面量的注入面。
func idSetInExpr(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	return "id IN (" + joinNonEmpty(marks) + ")", args
}

// idSetAnyExpr 构造「该行是否属于给定的行 id 集合」，返回可直接拼进 WHERE
// 的片段，参数追加到 args 之后。
func idSetAnyExpr(args *[]any, ids []int64, prefix string) string {
	marks := make([]string, len(ids))
	for i, id := range ids {
		*args = append(*args, id)
		if prefix == "" {
			marks[i] = fmt.Sprintf("$%d", len(*args))
		} else {
			marks[i] = prefix + fmt.Sprintf("$%d", len(*args))
		}
	}
	return "id IN (" + joinNonEmpty(marks) + ")"
}

// sqlIDListExpr 返回「<col> IN ($n,…)」，并把 ids 逐个追加到 *args。
//
// 为什么必须展开成 IN 列表而不是绑一个数组参数：
//
//   - SQLite 的驱动无法把 []int64 绑成单参（"unsupported type []int64, a slice
//     of int64"）；
//   - 展开后每个值仍各自绑定，不存在拼接字面量的注入面。
//
// 占位符编号由 *args 当前长度决定，所以调用点必须**先**把自己原有的参数追加
// 进去，再调用本函数——顺序反了会让 $n 指到别的参数上。
func sqlIDListExpr(args *[]any, col string, ids []int64) string {
	marks := make([]string, len(ids))
	for i, id := range ids {
		*args = append(*args, id)
		marks[i] = fmt.Sprintf("$%d", len(*args))
	}
	return col + " IN (" + joinNonEmpty(marks) + ")"
}

func joinNonEmpty(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
