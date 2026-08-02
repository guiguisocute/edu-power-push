package storage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* 排行榜身份展示与脱敏。见 frontend/docs/USER-PREFERENCES.md §5。
   榜单匿名可读。默认：完整栋号与楼层、房间打码。
   绑定账号可调整展示字段。所有电表强制参与。禁止退出。 */

func defaultLeaderboardPreference() LeaderboardPreference {
	// 默认完整栋号与楼层。房间打码。昵称默认不展示。
	return LeaderboardPreference{
		OptedIn:      true,
		ShowBuilding: true, ShowFloor: true, ShowRoom: true,
		MaskBuilding: false, MaskFloor: false, MaskRoom: true,
	}
}

var digitsPattern = regexp.MustCompile(`[0-9]+`)

// maskDigits 将数字段替换为 **。例如「12栋」→「**栋」。
// 楼栋楼层字面脏。仅打码数字。保留其余字面。
func maskDigits(value string) string {
	if value == "" {
		return value
	}
	masked := digitsPattern.ReplaceAllString(value, "**")
	if masked == value {
		// 无数字时整体隐去。避免成为唯一标识。
		return "**"
	}
	return masked
}

/*
rankingName 渲染榜单名称列。
	binderCount 为绑定人数。0 表示未注册。
	多人合用时显示「共享房间」。禁止写单一昵称。
	reveal 为管理员视角。不脱敏。列出全部昵称。
*/
func rankingName(binderCount int, showNickname bool, nicknames []string, reveal bool) string {
	if binderCount == 0 {
		return "未注册用户"
	}
	if reveal {
		named := make([]string, 0, len(nicknames))
		for _, n := range nicknames {
			if nick := strings.TrimSpace(n); nick != "" {
				named = append(named, nick)
			} else {
				named = append(named, "未命名")
			}
		}
		if len(named) == 0 {
			return "未命名"
		}
		return strings.Join(named, "、")
	}
	if binderCount > 1 {
		return "共享房间"
	}
	nick := ""
	if len(nicknames) > 0 {
		nick = strings.TrimSpace(nicknames[0])
	}
	if showNickname && nick != "" {
		return nick
	}
	return "匿名用户"
}

// rankingLocation 按偏好渲染位置文案。昵称不进本列。
// 全部位置字段关闭时返回空串。
func rankingLocation(pref LeaderboardPreference, building, floor, room string, reveal bool) string {
	// 管理员视角：完整位置。不受展示与打码开关影响。
	if reveal {
		pieces := make([]string, 0, 3)
		for _, v := range []string{building, floor, room} {
			if v != "" {
				pieces = append(pieces, v)
			}
		}
		return strings.Join(pieces, " · ")
	}
	pieces := make([]string, 0, 3)
	if pref.ShowBuilding && building != "" {
		if pref.MaskBuilding {
			pieces = append(pieces, maskDigits(building))
		} else {
			pieces = append(pieces, building)
		}
	}
	if pref.ShowFloor && floor != "" {
		if pref.MaskFloor {
			pieces = append(pieces, maskDigits(floor))
		} else {
			pieces = append(pieces, floor)
		}
	}
	if pref.ShowRoom && room != "" {
		if pref.MaskRoom {
			pieces = append(pieces, maskDigits(room))
		} else {
			pieces = append(pieces, room)
		}
	}
	return strings.Join(pieces, " · ")
}

func GetLeaderboardPreference(ctx context.Context, pool *pgxpool.Pool, userID string) (LeaderboardPreference, error) {
	pref := defaultLeaderboardPreference()
	err := pool.QueryRow(ctx, `
		SELECT opted_in,show_building,show_floor,show_room,show_nickname,
			mask_building,mask_floor,mask_room,updated_at
		FROM user_leaderboard_preferences WHERE user_id=$1::uuid
	`, userID).Scan(
		&pref.OptedIn, &pref.ShowBuilding, &pref.ShowFloor, &pref.ShowRoom,
		&pref.ShowNickname, &pref.MaskBuilding, &pref.MaskFloor, &pref.MaskRoom, &pref.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultLeaderboardPreference(), nil
	}
	if err != nil {
		return pref, fmt.Errorf("read leaderboard preference: %w", err)
	}
	// 强制参与。对外始终 true。库中旧 false 无效。
	pref.OptedIn = true
	return pref, nil
}

func SaveLeaderboardPreference(ctx context.Context, pool *pgxpool.Pool, userID string, pref LeaderboardPreference) (LeaderboardPreference, error) {
	// 所有电表强制上榜。忽略客户端 opted_in。
	pref.OptedIn = true
	var saved LeaderboardPreference
	err := pool.QueryRow(ctx, `
		INSERT INTO user_leaderboard_preferences (
			user_id,opted_in,show_building,show_floor,show_room,show_nickname,
			mask_building,mask_floor,mask_room,updated_at
		) VALUES ($1::uuid,true,$2,$3,$4,$5,$6,$7,$8,now())
		ON CONFLICT (user_id) DO UPDATE SET
			opted_in=true, show_building=EXCLUDED.show_building,
			show_floor=EXCLUDED.show_floor, show_room=EXCLUDED.show_room,
			show_nickname=EXCLUDED.show_nickname, mask_building=EXCLUDED.mask_building,
			mask_floor=EXCLUDED.mask_floor, mask_room=EXCLUDED.mask_room, updated_at=now()
		RETURNING opted_in,show_building,show_floor,show_room,show_nickname,
			mask_building,mask_floor,mask_room,updated_at
	`,
		userID, pref.ShowBuilding, pref.ShowFloor, pref.ShowRoom,
		pref.ShowNickname, pref.MaskBuilding, pref.MaskFloor, pref.MaskRoom,
	).Scan(
		&saved.OptedIn, &saved.ShowBuilding, &saved.ShowFloor, &saved.ShowRoom,
		&saved.ShowNickname, &saved.MaskBuilding, &saved.MaskFloor, &saved.MaskRoom, &saved.UpdatedAt,
	)
	if err != nil {
		return saved, fmt.Errorf("save leaderboard preference: %w", err)
	}
	saved.OptedIn = true
	return saved, nil
}
