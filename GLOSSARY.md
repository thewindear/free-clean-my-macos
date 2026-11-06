# free-clean-my-macos

A macOS junk scanner and cleaner: it finds only files that are provably safe to delete, groups them by the software that owns them, and lets the user choose what to remove.

## Language

### Scanning

**Junk Item**（垃圾项）:
One discovered file or directory that a Scan Rule claims is removable, with its absolute path, size, Kind, Risk Level and Owner.
_Avoid_: 垃圾文件, 临时文件

**Display Path**（显示路径）:
How a Junk Item's Absolute Path is shown: `$HOME` abbreviated to `~` to fit the list, full absolute path in the detail view. A Junk Item always has a real path — no entry is ever an unnamed aggregate.
_Avoid_: 相对路径, 简写路径

**Rule Precedence**（规则优先级）:
The longest matching path pattern wins, so a specific rule (`~/Library/Caches/go-build`) overrides the generic one (`~/Library/Caches/*`). A path belongs to exactly one Junk Item: overlapping rules never double-count bytes.
_Avoid_: 规则覆盖

**Scan Rule**（扫描规则）:
A declarative rule — absolute path pattern, Owner, Kind, Risk Level, Rebuild Cost — and the only thing that can produce a Junk Item. Rules are written as full paths, never as bare directory names. Nothing is discovered by guessing.
_Avoid_: 黑名单, 匹配规则

**Owner**（归属者）:
The software entity that owns a Junk Item: an app, a developer toolchain, or the system itself. Scan results are aggregated per Owner.
_Avoid_: 应用, App（当范围不止 .app 时）

**Kind**（类别）:
Where a Junk Item comes from: `cache`, `log`, `dev-residue`, `temp`, `orphan`. The Trash is not a Kind: it is unreadable without Full Disk Access and is emptied by Finder instead.
_Avoid_: 类型, 分类

**Orphan**（卸载残留）:
A Junk Item whose Owner is no longer installed on this machine.

### Judging

**Risk Level**（风险级）:
`safe` means deleting it leaves the system and its software fully functional; `caution` means deleting it costs a feature or needs a human decision. This replaces the vague phrase "可清理".
_Avoid_: 可清理, 安全/危险

**Rebuild Cost**（重建代价）:
What it costs to get the data back after deletion: `none` (nothing to rebuild), `low` (rebuilt in seconds by the app), `high` (rebuild takes hours or a large download). A binary "rebuildable" is not precise enough to drive the default.
_Avoid_: 可重建

**Recommended**（推荐清理）:
Whether a Junk Item is pre-checked. Derived from Risk Level, Rebuild Cost, whether its Owner is running, and its size.
_Avoid_: 建议删除, 自动勾选

**Skip**（跳过）:
A path excluded before it becomes a Junk Item, always carrying a reason: no permission, Owner running, protected system area, or user rule.
_Avoid_: 忽略, 过滤

### Cleaning

**Cleanup**（清理）:
Removing the selected Junk Items — `risk=safe` + `rebuild_cost=low` items are deleted outright, everything else is moved into the Trash so it stays recoverable.
_Avoid_: 删除, 清除

**Removal Mode**（移除方式）:
How one Junk Item leaves the disk during a Cleanup: `delete` (outright, counted toward Space Freed) or `trash` (moved, counted toward Space Moved). Derived from Risk Level and Rebuild Cost by the hybrid policy — it is not a per-item user choice; Permanent Delete is the only override.
_Avoid_: 删除方式, mode（无限定词）

**Outcome**（结果）:
What actually happened to one Junk Item: `planned` in a dry run, then `deleted`, `trashed` or `failed`. Every item gets exactly one Outcome and exactly one Cleanup Log record.
_Avoid_: 状态, 结果状态

**Space Freed**（已释放）:
Bytes actually reclaimed — only items deleted outright count. Moving into the Trash frees nothing until the Trash is emptied.
_Avoid_: 已清理空间

**Space Moved**（已移入废纸篓）:
Bytes sitting in the Trash after a Cleanup: recoverable, and only freed once the user empties the Trash in Finder.
_Avoid_: 已释放（用于这部分数据）

**Permanent Delete**（永久删除）:
An explicitly requested mode that bypasses the Trash and removes items irrecoverably; needs a second confirmation.
_Avoid_: 彻底删除, 强删

**Cleanup Log**（清理日志）:
The append-only `jsonl` record of every Cleanup: path, size, Owner, Risk Level, outcome, timestamp.
_Avoid_: 操作记录

**Self-Exclusion**（自我排除）:
An engine-level invariant: the tool's own binary, config, state and log paths can never become Junk Items, whatever the rules or a user override says.
_Avoid_: 忽略自己

**User Ignore List**（用户忽略清单）:
A user-owned list of path patterns that must stay out of every scan, layered on top of the Scan Rules.
_Avoid_: 黑名单

### Interface

**Scan Progress**（扫描进度）:
Live feedback while scanning — items visited and bytes found so far — cancelled with a single key.
_Avoid_: 加载中

**Selection**（勾选集合）:
The Junk Items currently checked, with a live count and byte total. The sizes are the engine's; the list only sums them.
_Avoid_: 选中列表

**Fold**（折叠）:
Replacing the Junk Items under 10 MB of one Owner with a single "N other items" row so the list stays readable. Folding never changes the Selection: the row stands for its Junk Items.
_Avoid_: 聚合, 合并

**Detail**（详情）:
The panel one Junk Item or Owner opens into: the Scan Rule that matched, what its Risk Level and Rebuild Cost mean, and the absolute path.
_Avoid_: 属性页

**Cleanup Progress**（清理进度）:
Per-Junk-Item outcome (pending / done / failed) plus an overall byte counter. Since Trash moves are near-instant, this is a checklist rather than a slow bar.
_Avoid_: 进度条

**Summary**（清理结果摘要）:
The end-of-run report: Space Freed and Space Moved reported separately, items cleaned, items failed with reasons, and the Cleanup Log path.
_Avoid_: 完成提示
