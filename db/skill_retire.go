package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/Autumn-27/artex/skills"
)

// 内置技能库整体换成 usestrix/strix 之后，存量库里还留着 830 个已下线技能名
// 的引用。它们指向的技能在磁盘上已经不存在，留着不只是"脏"，会转成三类具体故障：
//
//  1. agent_skill_visibility：830 × 5 个 agent ≈ 4150 行。更致命的是播种闸门
//     builtin_skill_visibility_v3 已经是 done，不清掉它，新的 75 个技能永远
//     不会被播种 —— 全部内置技能对 agent 不可见，且没有任何报错。
//  2. skill_usage 里 found=false 的行：SkillGaps 只统计这类行，于是 830 个
//     刚被主动下线的技能会被报成"被反复请求但不存在"的缺口，等于让系统把它们
//     再建回来。found=true 是真实调用历史，保留不动。
//  3. skill_proposals 里 status=pending 的提议：审阅台会提示为已废弃的技能建包。
//     否决而不是删除 —— 提议表的存在意义就是留审阅记录，删掉就查无对证了。
//
// 只清理 skills.RetiredSkillNames() 列出的名字。用户自建、改名的技能不在这份
// 名单里，一行都不会被碰。

const (
	// skillRefsRetiredGate 是本迁移自己的一次性闸门。
	skillRefsRetiredGate = "skill_refs_retired_v1"
	// builtinVisibilityGate 是 seedBuiltinSkillVisibility 的闸门。迁移要把它
	// 清掉，播种才会为新技能重跑一次。
	builtinVisibilityGate = "builtin_skill_visibility_v3"
)

// skillRetireNote 写在被自动否决的提议上，让审阅者看到是谁、为什么动的。
const skillRetireNote = "内置技能库已切换为 usestrix/strix，该技能已下线，自动否决"

// SkillRetireReport 是一次清理的结果，供启动日志汇报。
type SkillRetireReport struct {
	Visibility  int64 // 删除的可见性行数
	UsageMisses int64 // 删除的"未命中"使用记录行数
	Proposals   int64 // 自动否决的待审阅提议数
}

// retireSkillRefs 清掉存量库里已下线内置技能名的所有引用。一次性（闸门
// skillRefsRetiredGate）；新库与已迁移的库是空操作。
//
// 必须早于 seedBuiltinSkillVisibility：它清掉播种闸门，播种紧接着为新技能重跑
// 一遍，两步之间不留"技能全不可见"的窗口。因此调用点排在 seedBuiltins 之前。
func (d *DB) retireSkillRefs() (SkillRetireReport, error) {
	var rep SkillRetireReport
	if v, _, _ := d.GetSetting(skillRefsRetiredGate); v == "done" {
		return rep, nil
	}
	names := skills.RetiredSkillNames()
	if len(names) == 0 {
		return rep, d.SetSetting(skillRefsRetiredGate, "done")
	}
	// 提议表是懒建的。老库如果从没产生过提议，这里先建出来，免得下面的 UPDATE
	// 打到不存在的表上；已存在时是空操作。
	if err := d.ensureSkillProposalTable(); err != nil {
		return rep, fmt.Errorf("ensure skill_proposals: %w", err)
	}
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return rep, err
	}
	defer func() { _ = tx.Rollback() }()

	// 固定参数排在 IN 列表之前，execInChunks 按这个顺序编号占位符。
	del := func(tmpl string, args ...any) (int64, error) {
		return execInChunks(ctx, tx.ExecContext, tmpl, names, args...)
	}

	if rep.Visibility, err = del(`DELETE FROM agent_skill_visibility WHERE skill_name IN (%s)`); err != nil {
		return rep, fmt.Errorf("clear retired visibility: %w", err)
	}
	if rep.UsageMisses, err = del(`DELETE FROM skill_usage WHERE found=false AND skill IN (%s)`); err != nil {
		return rep, fmt.Errorf("clear retired misses: %w", err)
	}
	if rep.Proposals, err = del(
		`UPDATE skill_proposals SET status=$1, note=$2, reviewed_ts=now()
 WHERE status=$3 AND skill IN (%s)`,
		SkillProposalRejected, skillRetireNote, SkillProposalPending); err != nil {
		return rep, fmt.Errorf("reject retired proposals: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return rep, err
	}
	// 清播种闸门，让 75 个新技能紧接着被播种。刻意放在事务外：播种本身幂等，
	// 即使这一步之后进程中断，下次启动也只是再播一次（ON CONFLICT DO NOTHING），
	// 而把闸门的清与置放进同一个事务会让"迁移做了但闸门没开"无从恢复。
	if err := d.SetSetting(builtinVisibilityGate, ""); err != nil {
		return rep, fmt.Errorf("reset visibility gate: %w", err)
	}
	if err := d.SetSetting(skillRefsRetiredGate, "done"); err != nil {
		return rep, fmt.Errorf("mark retire gate: %w", err)
	}
	if rep.Visibility+rep.UsageMisses+rep.Proposals > 0 {
		log.Printf("[db] 已下线内置技能的存量引用已清理：可见性 %d 行、未命中记录 %d 行、待审阅提议 %d 条",
			rep.Visibility, rep.UsageMisses, rep.Proposals)
	}
	return rep, nil
}

// execFunc 匹配 *sql.Tx / *sql.Conn / *sql.DB 的 ExecContext，让分批执行可以
// 挂到任意一个上。
type execFunc func(ctx context.Context, query string, args ...any) (sql.Result, error)

// execInChunks 按 IN 列表分批执行同一条模板语句，返回累计影响行数。
//
// tmpl 里必须且只能有一个 %s，它是占位符列表的位置；固定参数 args 排在 IN 列表
// 之前，所以它们占 $1..$len(args)。
//
// 分批不是绕 SQLite 的变量上限（默认 32766，843 个一次也够），而是让单条语句
// 保持在常规规模：解析器要为 IN 的每一项建一次索引查找，一条语句塞八百多项会让
// 执行计划与错误信息都难以阅读，分批后出错能定位到具体批次。
func execInChunks(ctx context.Context, exec execFunc, tmpl string, names []string, args ...any) (int64, error) {
	const chunk = 200
	var total int64
	base := len(args)
	for start := 0; start < len(names); start += chunk {
		end := start + chunk
		if end > len(names) {
			end = len(names)
		}
		batch := names[start:end]
		placeholders := make([]string, len(batch))
		in := make([]any, 0, len(args)+len(batch))
		in = append(in, args...)
		for i, name := range batch {
			placeholders[i] = fmt.Sprintf("$%d", base+i+1)
			in = append(in, name)
		}
		res, err := exec(ctx, fmt.Sprintf(tmpl, strings.Join(placeholders, ",")), in...)
		if err != nil {
			return total, err
		}
		// RowsAffected 的 error 只表示驱动不支持统计，此时按"未知"计 0 继续，
		// 不能当作失败 —— 清理已经发生了。
		if n, err := res.RowsAffected(); err == nil {
			total += n
		}
	}
	return total, nil
}
