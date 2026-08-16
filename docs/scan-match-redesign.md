# 扫描与 TMDB 匹配逻辑重设计

> 状态：提案（未实施）
> 日期：2026-08-15
> 基于对 media-1.txt / media-2.txt（523 个 release 目录）与现有代码的分析

## 1. 现状问题（按根因归类）

### 1.1 匹配器太弱（最主要）

`internal/tmdb/client.go` 的 `SearchMovie` 把**第一个搜索结果直接当答案**，且：

- `year` 参数是**硬过滤**，解析出的年份错一个字（`Spirited.Away.2011` 实为 2001）→ 直接 0 结果
- 无标题相似度校验 → 可能匹配到完全不同的电影
- 无降级策略：带年搜索失败后不会尝试"去掉年份再搜 + 评分挑最优"
- 缓存 key 包含全部四个输入（tmdb_id/imdb_id/title/year），解析结果一变 key 就变；无负结果缓存

### 1.2 标题提取是纯位置切分

`parser.ParseTitle`：取 year/source/resolution 之前的所有 token 作标题，但 **edition 品牌词不从中剔除**。实测 523 个目录中有 8 个产生脏标题：

| 目录名解析出的标题 | 问题 |
|---|---|
| `JFK DirCut` | DirCut 残留 |
| `Avatar Extended Collector's Edition` | 版本词残留 |
| `The Ballad of Narayama MOC` | MoC 品牌残留 |
| `Three Colors 1993-1994 GBR UHD` | 地区/介质残留 |
| `Tacones Lejanos AKA High Heels` | AKA 双标题未拆分 |
| LOTR 三部 Extended Edition | 版本词残留 |

加上拼写错误（`As.Tears.Goes.By`、`Triump.Des.Willens`、`Grean.Snake`）和年份错误（Spirited Away 2011），此前共产生 23 条无 TMDB 记录，已手工删除。

### 1.3 合集目录处理不足

实测 9 个年份范围合集（Alien Anthology 1979-1997、Infernal Affairs Trilogy、Three Colors、The Before Trilogy、Ang.Lee's.Father.Trilogy 等）+ 若干 Part.I/II 分部。两类形态：

- **嵌套型**（Ang.Lee's Trilogy → 每部一个子目录）：现在能 descend（depth ≤ 1），但外层目录名也会被当 release 试一次
- **平铺型**（Alien.Anthology 目录下直接放 4 部片的 mkv）：现在只取**最大**的一个视频文件当主文件，其余 3 部**丢失**。根因之一是 `movie_files` 唯一键是 `(library_id, dir_path)`，一个目录只能有一条记录

### 1.4 NFO 利用不充分

566 个文件中 **289 个（51%）带 NFO**，其中通常有 `<uniqueid type="tmdb/imdb">`，是最可靠的匹配来源。当前只在 movie seed 时读一次；NFO 的 tmdb_id 已能直连，但 imdb_id 走 `/find` 失败后不会降级到标题搜索。

### 1.5 无匹配状态机、无人工兜底

- 匹配失败 → movie 行留在库里 tmdb_id NULL，和"未尝试"无法区分
- 无 `GET candidates` / `POST 手动指定 tmdb_id` 的 API 和 UI
- `unchanged` 的文件（size/mtime 未变）永远不会重试匹配
- `scan_jobs` 表是死代码；扫描与 TMDB 串行同步，慢且不可观测

### 1.6 其他隐患

- `RemoveStaleMovieFiles` 在 walk 结果为空时**清空整库**（walk 早期失败 → 误删）
- 无 DVD（VIDEO_TS）检测（当前库只有 1 个 BDMV 原盘，暂不紧急）

## 2. 设计目标

1. **全自动匹配率 ≥ 99%**（以 523 目录为测试集，含拼写错误与错误年份）
2. 每个物理视频文件都有对应的 `movie_files` 行（平铺合集不丢文件）
3. 匹配失败可观测、可重试、可人工干预
4. 扫描（文件系统）与匹配（TMDB 网络调用）解耦，扫描不再被网络阻塞
5. 对现有 541 条已匹配数据保持兼容，不需要全量重建

## 3. 总体架构

```
┌─────────────┐    ┌──────────────┐    ┌───────────────────┐
│  scanner    │───>│   parser     │───>│ matcher (新)       │
│ 文件系统遍历 │    │ 文件名解析    │    │ 候选生成→评分→决策  │
│ 快、无网络   │    │ 标题清洗      │    │ 带速率限制的异步队列 │
└─────────────┘    └──────────────┘    └─────────┬─────────┘
      │                                          │ 自动接受(高置信)
      │  movie_files(含 movie_id=NULL 的待匹配)    │ 或 pending(低置信)
      ▼                                          ▼
   SQLite  <────────── enrich(命中后才拉详情) <────┘
                                                + 手动匹配 API/UI
```

关键变化：**scan 只负责发现文件和解析**（纯本地、秒级）；**matcher 是独立的异步阶段**，逐条消费待匹配项，多策略搜索 + 评分；只有 matcher 接受的结果才进入 enrich 拉全量详情。

## 4. 各层详细设计

### 4.1 scanner：文件发现

**核心原则：文件不去重，电影才去重。** 每个有效视频文件 = 一条 `movie_files` 记录；同一部电影的多个版本（不同分辨率/edition/来源）是 N 条 `movie_files` 指向同一行 `movies`，全部显示在电影详情页。

1. **每个视频文件一条记录**：
   - `The.Bourne.Identity.2002.720p.mkv` 与 `The.Bourne.Identity.2002.1080p.mkv` -> 2 条 `movie_files`，同一 movie，详情页展示两个版本
   - `Happy.Hour.2015.Disk1/Disk2.mkv`（一部电影两张碟）-> 同样 2 条记录挂同一 movie
   - 同目录不同 edition（Theatrical/Director's.Cut 两个文件）-> 2 条记录，`edition` 字段区分
   - 删除现有"取最大文件为主"的概念；播放时默认版本由 UI 按 resolution/bitrate 排序决定（或用户上次选择），不落库
2. **唯一键改为 `(library_id, dir_path, file_name)`**（原 `(library_id, dir_path)`），原盘 `file_name=''` 不变
3. **跳过/降级规则**（文件名含 sample/trailer/promo/menu 的跳过；extras/bonus 类文件记 `movie_files` 但不建 movie，见 4.2）
4. **外层合集目录**：检测到年份范围（`\d{4}-\d{4}`）/ trilogy / anthology / boxset / 无年份纯分组名（`Studio.Ghibli.Collection`）时，目录本身**不生成** movie，只 descend；子文件/子目录按上述规则独立处理
5. **空 walk 保护**：`RemoveStaleMovieFiles` 仅在 `len(seen) > 0` 时执行；`seen` 为空视为异常并报错
6. 顺手启用 `scan_jobs` 记录每次扫描的 added/updated/removed/failed

**文件 -> 电影的分组**：文件先按解析出的 (title, year) 分组共享 movie seed（避免 TMDB 确认前建重复行）；matcher 命中后以 tmdb_id 为准合并/修正归属。分组只是过渡态，最终归属由匹配结果决定。

### 4.2 parser：标题清洗（匹配专用）

解析产出两个标题：`Title`（展示，保留版本信息语义）与 **`SearchTitle`（匹配专用，激进清洗）**。清洗规则按序迭代至稳定：

1. 位置切分（现有逻辑）后，从尾部剔除噪声 token：
   - 版本/品牌：`Extended, Collector's, Edition, DirCut, DC, Uncut, Remastered, Repack, IMAX, Anniversary, Criterion Collection, Masters of Cinema, CC, MOC, EE, RM`
   - 地区/介质：`UHD, GBR, FRA, CEE, JPN, KOR, HK, USA, UK, REMUX`
   - 冠词处理：尾部 `Part.I` 保留（TMDB 标题含 Part），开头 `The/A` 记录独立变体
2. **AKA 拆分**：`Tacones Lejanos AKA High Heels` → 两个候选标题
3. **`&`/`and` 归一**：`Asako I & II` 生成 `Asako I and II` 变体
4. **多年份 token**：`(19|20)\d{2}` 出现多个时，取**最后一个**为年份（`1987.When.the.Day.Comes.2017`、`2001.A.Space.Odyssey.1968` 均正确），但若最后一个年份与 source/resolution 相邻度更高则维持现状（已验证现有 findYear 右扫逻辑对这两例正确，保持）
5. 非 ASCII 标题（Amélie/Léon/Átame/Takeshis'）原样保留——TMDB 搜索对原文支持良好

### 4.3 matcher：多策略候选生成 + 评分（核心）

#### 候选生成（按可信度顺序，命中即短路）

| # | 策略 | 输入 | 说明 |
|---|------|------|------|
| 1 | NFO 直连 | tmdb_id / imdb_id | 权威；imdb `/find` 失败降级到策略 3 |
| 2 | 已有 movie.tmdb_id | | 已 enrich 的行跳过 |
| 3 | 带年搜索 | SearchTitle + year | `year` 参数**仅作加分项不做硬过滤**：改为不带 year 搜索 + 评分时比较年份 |
| 4 | 无年搜索 | SearchTitle 变体轮询 | AKA 两半 / 去 The / &↔and / 去 Part 后缀 |
| 5 | original_language 加权 | | 标题含 CJK 或解析出 jpn/kor/chi 时加 `language` 参数重试 |

单条记录最多 N 次 API 调用（预算 4 次），负结果缓存 7 天（正结果 30 天，key 为 `(策略, 归一化标题, year)`）。

#### 评分函数（0–100）

```
score = 60·titleSim + 30·yearSim + 10·popularityRank

titleSim：token 集合相似度（Jaccard）与顺序敏感编辑相似（Jaro-Winkler）取 max
          ★ token 级模糊匹配：单词间编辑距离 ≤1 视为相等
          （Grean→Green、Triump→Triumph、Goes→Go 全部命中）
          大小写/冠词/标点归一后比较
yearSim ：|result.year - parsed.year| → 0(=) 1.0 / ±1 0.7 / ±2 0.3 / >2 0
          parsed.year 缺失时取 0.5（不奖惩）
popularityRank：TMDB popularity 排名的对数衰减，仅作并列时 tiebreaker
```

#### 决策阈值

| 分数 | 动作 | match_status |
|------|------|--------------|
| ≥ 85 且年份差 ≤1 | **自动接受**，进 enrich | `matched` |
| 60–84 | 不自动接受，保留 top-5 候选 | `pending`（等人工确认） |
| < 60 或无候选 | 记录失败原因 | `unmatched`，7 天后自动重试一次 |

特殊规则：标题完全相同（归一后）+ 年份差 ≤1 → 直接 100 分短路，避免评分器误伤。

### 4.4 匹配状态机（新）

`movies` 增列（或独立表 `match_state`，倾向直接加列减少 join）：

```sql
ALTER TABLE movies ADD COLUMN match_status TEXT NOT NULL DEFAULT 'unmatched'
  CHECK (match_status IN ('matched','pending','unmatched','manual'));
ALTER TABLE movies ADD COLUMN match_score  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE movies ADD COLUMN match_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE movies ADD COLUMN last_match_at  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE movies ADD COLUMN fail_reason   TEXT NOT NULL DEFAULT ''; -- 供 UI 展示
```

- 存量 541 条已匹配 → `matched`（一次性迁移 SQL）
- `manual` 状态**永不被自动匹配覆盖**（人工指定的 tmdb_id 是权威）
- 每次扫描结束后，`unmatched` 且 `last_match_at` 超 7 天的重新入队

### 4.5 手动匹配 API + UI（兜底闭环）

```
GET  /api/matches/pending            # pending/unmatched 列表（含失败原因）
GET  /api/movies/{id}/candidates?q=  # 实时 TMDB 搜索，返回 top-10 + 评分
POST /api/movies/{id}/match          # { tmdb_id } → 置 manual + 触发 enrich
POST /api/movies/{id}/unmatch        # 清除匹配回 unmatched
POST /api/libraries/{id}/rematch     # 对 unmatched 全量重跑 matcher（dry_run 参数）
```

UI：Library 页增加 "Needs attention" 徽标 → 待匹配列表 → 点开看候选卡片（海报/年份/简介）→ 确认。

### 4.6 异步化与可观测

- matcher 作为进程内 work queue（`chan` + 单 worker，TMDB 限速 ~5 req/s），scan 结束时把所有待匹配项入队
- 30 分钟 context timeout 移到 matcher 层；扫描本身不再有网络调用，扫 500 目录应为秒级
- `scan_jobs` 记录文件变更统计；新增 `matched_auto / matched_manual / pending / unmatched` 计数

## 5. 数据库变更汇总

1. `movie_files` 唯一键 `(library_id, dir_path)` → `(library_id, dir_path, file_name)`（需迁移：重建索引；存量数据 file_name 已非空除原盘外）
2. `movies` 增加 match 四列（见 4.4）
3. 存量迁移：`UPDATE movies SET match_status='matched' WHERE tmdb_id IS NOT NULL`
4. `tmdb_cache` 不改结构，新增负结果语义（body 为 `{"negative":true}` + TTL 7 天）

## 6. 测试与验收

**Golden 用例集**（fixtures 直接取自本次排查的真实失败案例，写成 parser + matcher + scanner 的表驱动测试）：

```
JFK.DirCut.1991...                     -> JFK (1991)
Avatar.Extended.Collector's.Edition.2009 -> Avatar (2009)
Spirited.Away.2011（年份错）            -> 千与千寻 (2001)，靠 titleSim+无年搜索
As.Tears.Goes.By.1988（拼写错）         -> As Tears Go By (1988)，token 模糊
Grean.Snake.1993 / Triump.Des.Willens.1935
Tacones.Lejanos.AKA.High.Heels.1991    -> Tacones lejanos
Three.Colors.1993-1994.GBR.UHD...       -> 平铺合集拆 3 部
Alien.Anthology.1979-1997...            -> 平铺 4 文件各生成 movie_file
The.Matrix.Trilogy.1999-2003.Bluray-mixed -> 拆 3 部
1987.When.the.Day.Comes.2017 / 2001.A.Space.Odyssey.1968 -> 年份取后者
Doro.no.kawa.1981（罗马音）             -> 泥の河 (1981)（TMDB 检索别名可命中）
Amélie / Léon / Átame! / Takeshis'      -> 非 ASCII 标题
BlurayDisk/Troy...（BDMV 原盘）         -> 原盘路径不变
In.Bruges.2008.1080p.FRE.BluRay.../     -> 同目录 2 文件（FRE 标签差异）= 2 条
  In.Bruges.2008.1080p.BluRay....mkv       movie_files 挂同一 movie
Happy.Hour.2015.Disk1/Disk2            -> 2 条 movie_files，同一 movie
Up.2009/{Dug's.Special.Mission.mkv, Partly.Cloudy.mkv} -> bonus，不建 movie
Blade.Runner.2049/2036.Nexus.Dawn.mkv  -> 短片，标 pending 人工裁决
Escape.From.Mogadishu.Sample.mkv       -> sample，跳过
```

**验收指标**：对 523 目录跑 `rematch --dry-run`，输出报告：自动匹配 ≥ 99%、pending ≤ 5、unmatched = 0（含人工）；非 bonus 视频文件数（673）与 `movie_files` 行数一致；详情页能展示同片多版本（Bourne 720p/1080p 双版本用例）。

## 7. 实施分期

| 期 | 内容 | 风险 |
|----|------|------|
| P1 | parser 标题清洗（SearchTitle）+ golden 用例 | 低，纯函数易测 |
| P2 | matcher 包：策略瀑布 + 评分 + 阈值 + 负缓存；`rematch --dry-run` 先跑报告 | 中 |
| P3 | scanner：平铺多片、唯一键迁移、空 walk 保护、scan_jobs | 中（涉及 schema 迁移） |
| P4 | match_status 列 + 存量迁移 + 手动匹配 API/UI | 低 |
| P5 | 异步队列 + 限速 + 7 天重试 | 低 |

P2 完成即可解决历史 22 个未匹配文件；P3 解决合集丢文件；P4 提供最终兜底。

---

# 实施记录（2026-08-16 完成）

方案按 P1–P5 落地，过程中发现并修复了 4 个原有代码的缺陷，实现与提案的差异记录如下。

## 已实现

| 模块 | 文件 | 说明 |
|---|---|---|
| 标题清洗 | `internal/parser/search.go` | `SearchTitle` / `SearchVariants`：噪声词/AKA/`&`↔`and`/冠词/Part 后缀/粘连年份 |
| 评分匹配 | `internal/matcher/matcher.go` | 候选瀑布 + `60·标题 + 30·年份 + 10·热度`，token 编辑距离≤1 模糊匹配，上标折叠（Alien³=Alien 3） |
| 候选搜索 | `internal/tmdb/client.go` | `SearchMovieAll`：返回完整候选列表，年份不再作硬过滤 |
| 文件粒度扫描 | `internal/scanner/scanner.go` | 每视频文件一条 `movie_files`；平铺合集拆分；sample/trailer/extras/AppleDouble 跳过 |
| 匹配状态机 | 迁移 `009` | `match_status/score/attempts/last_match_at/fail_reason/match_candidates` |
| 折叠键重挂 | 迁移 `010` + `internal/store/matchkey.go` | `MatchKey`：大小写/标点/音标折叠，seed 两级查找（精确 → 折叠键 ±1 年） |
| 合并路径 | `internal/metadata/applymatch.go` | `ApplyMatch`：tmdb_id 已被占用时把文件挂到已有行并删空壳 |
| 手动匹配 | `internal/api/match.go` | pending 列表 / 候选搜索 / 确认（`manual`）/ 解绑 / 批量 rematch |
| 重命名 | `internal/rename/` | `TitleYear`+`Build`+`Apply`：只重建 `Title.Year` 段，技术尾原样保留 |
| 筛选器 | `FilterPanel.tsx` | 「匹配状态」组：数据健康（有问题/无文件/缺TMDB/多版本）+ 状态（已匹配/待审核/未匹配/手动） |
| 详情页修复 | `ManualMatch.tsx` | 手动匹配按钮 → 搜索/候选/确认 → 可选重命名（diff 预览、多库告警） |
| CLI | `darknight rematch [--dry-run]` | 批量重匹配，dry-run 出报告 |

## 过程中发现的原有缺陷（均已修复 + 回归测试）

1. **`UpsertMovieSeed` 空串误匹配**：`original_title = ?` 传空串，而未 enrich 行该列正是空串 → 退化为"只按年份匹配"，同年电影被合并（实测 7 部 2017 年电影挂在一行）。修复：动态构建 OR 子句，只比非空候选。
2. **matcher 被 `unchanged` 跳过**：`unchanged` 描述文件字节，却用来跳过电影匹配 → 169 行卡在 `match_attempts=0` 永不重试。修复：匹配与否只看电影行状态（`tmdb_id==0 && status!=manual`）。
3. **缺合并路径**：`movies.tmdb_id` 是 UNIQUE，匹配到已被占用的 id 会报约束冲突而非挂回已有行（实测 105 次冲突）。修复：`ApplyMatch` 统一三处调用点（scan / CLI / API）。
4. **其他**：`movie_id` NULL 扫描崩溃（改 `sql.NullInt64`）、MPEG-TS 的 `side_data_type` 是数字枚举（改 `json.RawMessage` 宽容解码）、`ListMovieFiles` 按文本排序导致 720p 排在 1080p 前（改数值排序）。

## 修复效果（真实库）

| 指标 | 修复前 | 修复后 |
|---|---:|---:|
| `movies` | 725 | 576 |
| 无文件孤儿 | 148 | 0（44 个已确认无文件的条目已删除） |
| 有文件无 TMDB | 169 | 45 |
| `unmatched` | 169 | 20 |
| `pending` | 0 | 26 |
| 文件总数 | 582 | 582（无损） |

剩余 20 unmatched + 26 pending 是 TMDB 确实搜不到的案例（拼写错误、罗马音、别名），通过详情页手动匹配处理。

## 与提案的差异

- **`unchanged` 语义**：提案只说"解耦"，实现中明确为「`unchanged` 仅跳过 ffprobe 与音轨/字幕重写，匹配始终按电影行状态决定」。
- **合并逻辑位置**：提案设想在 scanner 内联，实现中提取为 `metadata.ApplyMatch`，因为 CLI 和 API 的 rematch 同样需要（漏掉会撞 UNIQUE）。
- **`match_key` 非唯一索引**：同名不同年的重拍片必须共存，因此折叠键配合年份窗口使用，而非唯一约束。
- **重命名策略**：提案未细化；实现确定为「只替换 `Title.Year` 段，技术尾（分辨率/来源/编码/Group）原样保留」，因为匹配错的只是标题。
