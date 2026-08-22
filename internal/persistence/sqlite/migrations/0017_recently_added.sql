-- 最近更新:监测 Emby 新增影视,在用户中心展示最近入库的条目。
-- request_id 记录命中求剧时被自动标记为「已入库」的 media_requests 行(0 = 未命中),
-- 条目本身以 Emby 条目 ID 为唯一键,重复扫描不会产生重复记录。
CREATE TABLE recently_added (
    emby_item_id TEXT PRIMARY KEY,
    tmdb_id INTEGER NOT NULL DEFAULT 0 CHECK (tmdb_id >= 0),
    media_type TEXT NOT NULL DEFAULT '' CHECK (media_type IN ('movie', 'tv', '')),
    title TEXT NOT NULL DEFAULT '',
    date_created TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL,
    request_id INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_recently_added_date ON recently_added(date_created DESC, emby_item_id DESC);