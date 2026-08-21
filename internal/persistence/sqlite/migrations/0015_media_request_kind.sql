-- 求剧记录区分「求整部」(full)与「催更补缺集」(missing):电视剧/动漫在库但缺集时
-- 用户提交的是 missing 型请求, episodes 保存人类可读的缺失集清单。
ALTER TABLE media_requests ADD COLUMN kind TEXT NOT NULL DEFAULT 'full';
ALTER TABLE media_requests ADD COLUMN episodes TEXT NOT NULL DEFAULT '';
