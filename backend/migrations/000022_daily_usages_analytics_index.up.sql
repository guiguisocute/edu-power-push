-- daily_usages 覆盖索引，供校园聚合走 index-only scan。
-- 前导列为 usage_date，按日期范围切分。

CREATE INDEX daily_usages_analytics_idx
    ON daily_usages (usage_date, meter_id)
    INCLUDE (usage_kwh);
