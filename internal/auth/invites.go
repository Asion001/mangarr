package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// ErrInviteInvalid covers unknown, used up and expired invites alike.
var ErrInviteInvalid = errors.New("this invite link isn't valid any more")

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// CreateInvite makes an invite link for a group; the token is only
// returned now (only its hash is stored).
func (s *Service) CreateInvite(ctx context.Context, groupID int64, note string, maxUses int, expires *time.Time, by int64) (string, *model.Invite, error) {
	if n, _ := s.db.NewSelect().Model((*model.Group)(nil)).Where("id = ?", groupID).Count(ctx); n == 0 {
		return "", nil, errors.New("no such group")
	}
	if maxUses <= 0 {
		maxUses = 1
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	inv := &model.Invite{TokenHash: hashToken(token), GroupID: groupID, Note: note, MaxUses: maxUses, ExpiresAt: expires, CreatedBy: by,
		CreatedAt: time.Now().UTC()}
	if _, err := s.db.NewInsert().Model(inv).Exec(ctx); err != nil {
		return "", nil, err
	}
	return token, inv, nil
}

// Invite returns a usable invite by token.
func (s *Service) Invite(ctx context.Context, token string) (*model.Invite, error) {
	var inv model.Invite
	if err := s.db.NewSelect().Model(&inv).Where("token_hash = ?", hashToken(token)).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInviteInvalid
		}
		return nil, err
	}
	if inv.Uses >= inv.MaxUses || (inv.ExpiresAt != nil && time.Now().After(*inv.ExpiresAt)) {
		return nil, ErrInviteInvalid
	}
	return &inv, nil
}

// Redeem creates an account with an invite (the use is counted first, so
// a single-use link can't be redeemed twice at once).
func (s *Service) Redeem(ctx context.Context, token string, in NewUser) (*model.User, error) {
	inv, err := s.Invite(ctx, token)
	if err != nil {
		return nil, err
	}
	res, err := s.db.NewUpdate().Model((*model.Invite)(nil)).Set("uses = uses + 1").
		Where("id = ? AND uses < max_uses", inv.ID).Exec(ctx)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrInviteInvalid
	}
	in.GroupID, in.CreatedBy = inv.GroupID, inv.CreatedBy
	u, err := s.CreateUser(ctx, in)
	if err != nil {
		// give the use back: the username was taken, the password too short…
		_, _ = s.db.NewUpdate().Model((*model.Invite)(nil)).Set("uses = uses - 1").Where("id = ?", inv.ID).Exec(ctx)
		return nil, err
	}
	return u, nil
}

// Admins counts enabled users in the Admins group.
func (s *Service) Admins(ctx context.Context, idb bun.IDB) (int, error) {
	if idb == nil {
		idb = s.db
	}
	n, err := idb.NewSelect().Model((*model.User)(nil)).
		Where("group_id IN (SELECT id FROM groups WHERE builtin = ?)", model.GroupAdmins).Where("disabled = ?", false).Count(ctx)
	return int(n), err
}
