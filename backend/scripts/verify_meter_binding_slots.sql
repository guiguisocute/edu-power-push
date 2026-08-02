-- 校验 migrations/000025。
-- 一块表最多 4 个绑定。
-- 第 5 个绑定必须插入失败。
-- 整段包在事务内，最后 ROLLBACK。
-- 仅校验，禁止落库。
BEGIN;

\i /tmp/000025_meter_binding_slots.up.sql

\echo '=== 1) 历史生效绑定都拿到了槽位（不应有 NULL）==='
SELECT count(*) FILTER (WHERE slot IS NULL) AS active_without_slot,
       count(*)                             AS active_total
FROM user_meter_bindings WHERE unbound_at IS NULL;

\echo '=== 2) 造 5 个账号去抢同一块表 ==='
CREATE TEMP TABLE probe_users AS
SELECT gen_random_uuid() AS id, n FROM generate_series(1,5) AS n;

INSERT INTO user_accounts (id,email,password_hash,nickname)
SELECT id, 'probe'||n||'@example.test', '$2a$10$abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMN', '室友'||n FROM probe_users;

-- 选取当前无绑定的电表。
CREATE TEMP TABLE probe_meter AS
SELECT m.id FROM meters m
WHERE m.active AND NOT m.excluded
  AND NOT EXISTS (SELECT 1 FROM user_meter_bindings b WHERE b.meter_id=m.id AND b.unbound_at IS NULL)
LIMIT 1;

\echo '--- 逐个绑定，第 5 个应当插入 0 行 ---'
DO $$
DECLARE
    u record;
    mid uuid;
    inserted int;
BEGIN
    SELECT id INTO mid FROM probe_meter;
    FOR u IN SELECT id, n FROM probe_users ORDER BY n LOOP
        -- 使用与 storage.bindMeterSlot 相同的语句。
        WITH ins AS (
            INSERT INTO user_meter_bindings (user_id,meter_id,slot)
            SELECT u.id, mid, s.slot
            FROM generate_series(1,4) AS s(slot)
            WHERE NOT EXISTS (
                SELECT 1 FROM user_meter_bindings b
                WHERE b.meter_id=mid AND b.unbound_at IS NULL AND b.slot=s.slot
            )
            ORDER BY s.slot
            LIMIT 1
            RETURNING 1
        )
        SELECT count(*) INTO inserted FROM ins;
        RAISE NOTICE '第 % 个账号: 插入 % 行', u.n, inserted;
    END LOOP;
END $$;

\echo '=== 3) 这块表最终的绑定情况（应为 4 行，槽位 1..4）==='
SELECT count(*) AS binders, min(slot) AS min_slot, max(slot) AS max_slot,
       CASE WHEN count(*)=4 AND min(slot)=1 AND max(slot)=4 THEN 'ok' ELSE '*** WRONG ***' END AS verdict
FROM user_meter_bindings
WHERE meter_id=(SELECT id FROM probe_meter) AND unbound_at IS NULL;

\echo '=== 4) 唯一索引确实挡得住手写的重复槽位 ==='
DO $$
DECLARE mid uuid; uid uuid;
BEGIN
    SELECT id INTO mid FROM probe_meter;
    SELECT id INTO uid FROM probe_users WHERE n=5;
    BEGIN
        INSERT INTO user_meter_bindings (user_id,meter_id,slot) VALUES (uid, mid, 1);
        RAISE NOTICE '*** WRONG *** 重复槽位竟然插进去了';
    EXCEPTION WHEN unique_violation THEN
        RAISE NOTICE 'ok 槽位唯一索引拦截了重复绑定';
    END;
END $$;

\echo '=== 5) 解绑一个之后能再绑进来（槽位可回收）==='
UPDATE user_meter_bindings SET unbound_at=now(), slot=NULL
WHERE meter_id=(SELECT id FROM probe_meter) AND unbound_at IS NULL
  AND slot=2;

DO $$
DECLARE mid uuid; uid uuid; inserted int;
BEGIN
    SELECT id INTO mid FROM probe_meter;
    SELECT id INTO uid FROM probe_users WHERE n=5;
    WITH ins AS (
        INSERT INTO user_meter_bindings (user_id,meter_id,slot)
        SELECT uid, mid, s.slot
        FROM generate_series(1,4) AS s(slot)
        WHERE NOT EXISTS (
            SELECT 1 FROM user_meter_bindings b
            WHERE b.meter_id=mid AND b.unbound_at IS NULL AND b.slot=s.slot
        )
        ORDER BY s.slot LIMIT 1
        RETURNING slot
    )
    SELECT count(*) INTO inserted FROM ins;
    IF inserted=1 THEN RAISE NOTICE 'ok 空出来的 2 号槽被回收';
    ELSE RAISE NOTICE '*** WRONG *** 解绑后仍然绑不进去'; END IF;
END $$;

\echo '=== 6) 榜单聚合：一块表只出一行，且取最严 ==='
-- 为两个室友写入相反的隐私设置。
-- 一个全开，一个全关。
INSERT INTO user_leaderboard_preferences (user_id,show_building,show_floor,show_room,show_nickname,mask_building,mask_floor,mask_room)
SELECT id, true,  true,  true,  true,  false, false, false FROM probe_users WHERE n=1;
INSERT INTO user_leaderboard_preferences (user_id,show_building,show_floor,show_room,show_nickname,mask_building,mask_floor,mask_room)
SELECT id, false, true,  true,  false, true,  false, true  FROM probe_users WHERE n=3;

SELECT b.meter_id,
    count(*)::int AS binder_count,
    bool_and(COALESCE(p.show_building, true))  AS show_building,
    bool_and(COALESCE(p.show_nickname, false)) AS show_nickname,
    bool_or (COALESCE(p.mask_building, false)) AS mask_building,
    bool_or (COALESCE(p.mask_room,     true))  AS mask_room
FROM user_meter_bindings b
JOIN user_accounts u ON u.id=b.user_id
LEFT JOIN user_leaderboard_preferences p ON p.user_id=b.user_id
WHERE b.unbound_at IS NULL AND b.meter_id=(SELECT id FROM probe_meter)
GROUP BY b.meter_id;

\echo '   期望：binder_count=4, show_building=f（1 号全开但 3 号关了）, show_nickname=f, mask_building=t, mask_room=t'

ROLLBACK;
