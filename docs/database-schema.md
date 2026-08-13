# 数据库表结构与数据模型

## 总体架构

整个系统使用 **SQLite**（纯 Go 驱动 `modernc.org/sqlite`，无需 cgo），采用 **WAL 模式**。数据库默认位于 `.data/darknight.db`。所有时间戳以 **Unix 秒**存储（`INTEGER`，`0` 表示未知）。

架构分为三层：

```
磁盘文件结构 (scanner 扫描)
       ↓ 解析文件名 + ffprobe
movie_files (物理文件) ──-> movies (逻辑电影) ←── TMDB 元数据 (enricher)
       ↓                         ↓
audio_tracks / subtitles    genres / people / credits / collections
                                 ↓
                            watch_status (用户状态)
```

**核心设计理念**：`movies` 是逻辑电影（一部电影一行），`movie_files` 是物理文件（同一部电影可以有多个版本/分辨率）。这是 1:N 关系。

---

## 迁移机制

迁移文件位于 `internal/store/migrations/`，通过 `//go:embed` 编译进二进制。启动时 `migrate()` 按文件名字典序执行未应用的 `.sql` 文件，并用 `schema_migrations` 表记录已执行的版本。目前有 8 个迁移（`001` ~ `008`）。

---

## 表结构详解

### 1. `libraries` - 媒体库根目录

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | INTEGER PK | 自增 ID |
| `name` | TEXT | 库名称（如"电影"） |
| `root_path` | TEXT UNIQUE | 磁盘根目录绝对路径 |
| `scan_interval` | INTEGER | 自动扫描间隔（秒），0 = 仅手动 |
| `last_scan_at` | INTEGER | 上次扫描 Unix 时间戳 |

一个 library 是一个被递归扫描的磁盘目录。Scanner 遍历它，发现 release 目录（包含视频文件或 BDMV 文件夹），每个 release 对应一个 `movie_files` 行。

---

### 2. `movies` - 逻辑电影

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | INTEGER PK | |
| `title` | TEXT | 标题（初始来自文件名解析或 .nfo） |
| `sort_title` | TEXT | 排序标题（小写，去除 the/a/an 前缀） |
| `original_title` | TEXT | TMDB original_title（原始语言标题） |
| `title_en` | TEXT | 英文标题 |
| `title_zh` | TEXT | 中文标题 |
| `year` | INTEGER | 年份 |
| `release_date` | TEXT | ISO 8601 日期 |
| `runtime` | INTEGER | 片长（分钟） |
| `synopsis` | TEXT | 简介 |
| `poster_path` / `backdrop_path` | TEXT | TMDB 图片路径 |
| `tmdb_id` | INTEGER UNIQUE | TMDB ID（唯一，用于去重和关联） |
| `imdb_id` | TEXT | IMDb ID |
| `vote_average` / `vote_count` | REAL/INT | TMDB 评分 |
| `collection_id` | INTEGER FK -> collections | 所属合集 |
| `country` | TEXT | 主制片国家（ISO code，旧字段） |
| `countries` | TEXT | 所有制片国家，逗号分隔（如 "JP,US"） |
| `original_language` | TEXT | 原始语言（ISO 639-1） |

**索引**：`sort_title`、`year`、`tmdb_id`、`imdb_id`、`country`、`original_language`

**双语标题逻辑**：UI 用 `original_title` 作为主标题，根据 `original_language` 决定副标题——英文电影显示中文副标题，中文电影显示英文副标题，其他语言两者都显示。

**`country` vs `countries`**：`country` 是单个 ISO code（migration 002 加的，用于过滤）；`countries` 是完整逗号分隔列表（migration 005 加的，用于展示所有国家）。过滤时两者都检查，兼容旧数据。

---

### 3. `movie_files` - 物理文件/发行版

这是最核心的表，存储每个物理 release 的技术元数据。**同一部电影可以有多个 `movie_files`**（不同分辨率、不同来源）。

| 列分类 | 列 | 说明 |
|---|---|---|
| **关联** | `movie_id` (FK->movies), `library_id` (FK->libraries) | movie_id 可为 NULL（未匹配时） |
| **路径** | `dir_path`, `file_name`, `raw_name` | release 目录、主文件名、原始名 |
| **类型** | `is_disc` | 1 = BDMV 蓝光原盘文件夹结构 |
| **文件状态** | `file_size`, `file_modified` | 用于增量扫描（size+mtime 不变则跳过 ffprobe） |
| **文件名解析** | `release_group`, `edition`, `source`, `resolution`, `video_codec`, `audio_codec`, `audio_channels`, `hdr`, `dolby_vision`, `bit_depth`, `audio_count`, `language`, `is_collection` | 来自 `parser.ParseTitle()` 解析 release 目录名 |
| **ffprobe 精炼** | `duration_sec`, `video_bitrate`, `frame_rate`, `width`, `height`, `container` | 来自 ffprobe，0 = 未探测 |
| **ffprobe 缓存** | `ffprobe_json`, `ffprobe_version`, `ffprobe_at` | 原始 JSON 缓存，version 变化时重新探测 |
| **字幕聚合** | `subtitle_languages`, `has_external_subtitle` | 逗号分隔的字幕语言列表（避免每行 JOIN subtitles） |
| **NFO** | `nfo_path` | 关联的 .nfo 文件路径 |

**唯一索引**：`(library_id, dir_path)` —— 一个 release 在一个库中只存在一行。

**过滤索引**：`resolution`、`source`、`video_codec`、`hdr` —— 这些是 UI 左侧面板的快速过滤维度。

**增量扫描逻辑**：scanner 用 `(file_size, file_modified, ffprobe_version)` 三元组判断是否需要重新探测。如果文件没变且 ffprobe 版本没升级，直接跳过 ffprobe 调用，复用缓存。原盘（`is_disc=1`）从不 ffprobe。

---

### 4. `audio_tracks` - 音轨

| 列 | 说明 |
|---|---|
| `movie_file_id` (FK->movie_files, CASCADE) | 所属文件 |
| `language`, `codec`, `channels`, `title` | 来自 ffprobe |
| `is_default`, `is_lossless`, `order` | 默认音轨、无损标记、流顺序 |

每次扫描时整个文件的音轨被 **全量替换**（`ReplaceAudioTracks` 先删后插）。

---

### 5. `subtitles` - 字幕

| 列 | 说明 |
|---|---|
| `movie_file_id` (FK->movie_files, CASCADE) | 所属文件 |
| `file_path` | 外挂字幕文件路径；内嵌为空 |
| `language`, `format` | 语言 code、格式（srt/ass/pgs...） |
| `is_embedded`, `is_default`, `order` | 内嵌/外挂、默认、顺序 |
| `file_size` | 外挂字幕文件大小（内嵌为 0） |

字幕来源有两个：ffprobe 探测到的内嵌流 + 磁盘上的外挂文件（`.srt`/`.ass` 等）。两者合并后全量替换。

---

### 6. `collections` - 系统合集

| 列 | 说明 |
|---|---|
| `tmdb_id` (UNIQUE) | TMDB 合集 ID |
| `name`, `poster_path`, `backdrop_path`, `overview` | TMDB 合集元数据 |

来自 TMDB 的合集（如"异形系列"、"回到未来"）。`movies.collection_id` 指向这里。

---

### 7. `collection_parts` - 合集成员

| 列 | 说明 |
|---|---|
| `collection_id` (FK->collections, CASCADE) | 所属合集 |
| `tmdb_id` | 成员电影的 TMDB ID |
| `title`, `original_title`, `release_date`, `poster_path`, `overview`, `vote_average` | 缓存的 TMDB 元数据 |
| `order` | TMDB parts 数组的原始顺序（上映顺序） |

**唯一约束**：`(collection_id, tmdb_id)`

缓存 TMDB `/collection/{id}` 的 parts 数组，用于 UI 展示合集中哪些电影"已有"、哪些"缺失"。`local_movie_id` 在查询时通过 JOIN `movies.tmdb_id` 动态计算，不存储。

---

### 8. `genres` + `movie_genres` - 类型（多对多）

标准的关联表：`movie_genres(movie_id, genre_id)` 联合主键，双方 CASCADE 删除。

---

### 9. `people` + `movie_credits` - 演职员

- `people`：`tmdb_id` UNIQUE，存储姓名和头像路径
- `movie_credits`：关联表，`role` 字段 CHECK 为 `'cast'` 或 `'crew'`
  - cast：有 `character`（角色名）和 `order`（演员表顺序）
  - crew：有 `job`（如 'Director', 'Writer'）
  - 联合主键 `(movie_id, person_id, role)`

---

### 10. `watch_status` - 观看状态

| 列 | 说明 |
|---|---|
| `movie_id` (PK, FK->movies CASCADE) | 每部电影一行 |
| `status` | `unwatched` / `watching` / `watched`（CHECK 约束） |
| `progress` | 0~1 进度 |
| `last_played_at` | 上次播放时间 |
| `rating` | 0（无）或 1~10 |

单用户设计，`movie_id` 直接作为主键。

---

### 11. `user_collections` + `user_collection_items` - 用户自定义合集

用户自建的主题合集（如"库布里克"、"赛博朋克"），与系统 `collections` 独立。`user_collection_items` 带排序 `position` 和 `note`，`(collection_id, movie_id)` 唯一约束。

---

### 12. `tmdb_cache` - TMDB 响应缓存

| 列 | 说明 |
|---|---|
| `endpoint` (PK) | 规范化的 API 路径（如 `/movie/123?language=zh-CN`） |
| `body_json` | 原始 JSON 响应体 |
| `fetched_at` | 获取时间戳 |

允许离线操作，避免 TTL 内重复请求 TMDB。

---

### 13. `scan_jobs` - 扫描历史

记录每次扫描的统计信息（added/updated/removed）和状态（running/completed/failed），用于可观测性。

---

## 数据流：从磁盘到数据库

```
1. ScanLibrary() 遍历 library.root_path
2. classifyRelease(dir) 判断：
   - 目录含 BDMV 子目录 -> kindDisc（原盘）
   - 目录含视频文件 -> kindFile，选最大的文件
3. parser.ParseTitle(dirName) 解析文件名 -> FileMeta
   (title, year, source, resolution, codec, hdr, ...)
4. scanSideFiles(dir) 找 .nfo 和外挂字幕
5. buildMovieFile() 组装 MovieFile 结构
   - 原盘覆盖 source = "Bluray Disk"
6. upsertMovieFromRelease()：
   - 优先用 .nfo 的 TMDBID/IMDBID 匹配已有 movie
   - 否则用 (title, year) 匹配
   - 创建/更新 movies 行
   - 如果 Enricher 启用，调 TMDB 补全元数据
7. ffprobe 探测（跳过未变化的/原盘）
   -> applyProbe() 填充 duration/bitrate/resolution/container
   -> probeAudioTracks() / probeSubtitleStreams()
8. UpsertMovieFile() 写入 movie_files
9. ReplaceAudioTracks() / ReplaceSubtitles() 全量替换子表
```

**增量机制**：如果 `(file_size, file_modified)` 不变且 `ffprobe_version` 匹配，整个 ffprobe 步骤被跳过，缓存的 JSON 和探测字段直接复用。

---

## 查询模式

`buildMoviesQuery()` 是列表查询的核心。它的设计要点：

1. **DISTINCT 去重**：因为 JOIN `movie_files` 会让一部多版本电影出现多次，用 `SELECT DISTINCT` 保证每部电影只出现一次
2. **按需 JOIN**：只有当过滤条件涉及技术维度（分辨率/来源/编码/HDR/字幕）时才 JOIN `movie_files`，否则只查 `movies` 表
3. **字幕过滤**：用子查询 `GROUP_CONCAT(DISTINCT language)` 聚合字幕语言，再 LIKE 匹配
4. **国家过滤**：用 `(',' || countries || ',') LIKE '%,JP,%'` 在逗号分隔列表中精确匹配
5. **观看状态过滤**：用 `EXISTS/NOT EXISTS` 子查询检查 `watch_status`
6. **排序**：支持 title/year/vote_average/added 四种，`sort_title` 默认去除 the/a/an 前缀
