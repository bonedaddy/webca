package service

import (
	"context"
	"fmt"

	"github.com/CzarSimon/httputil"
	"github.com/CzarSimon/httputil/jwt"
	"github.com/CzarSimon/webca/api-server/internal/audit"
	"github.com/CzarSimon/webca/api-server/internal/authorization"
	"github.com/CzarSimon/webca/api-server/internal/model"
	"github.com/CzarSimon/webca/api-server/internal/repository"
	"github.com/opentracing/opentracing-go"
)

// UserService service responsible for user business logic.
type UserService struct {
	AuditLog    audit.Logger
	UserRepo    repository.UserRepository
	AuthService *authorization.Service
}

// GetUser retrieves users from database if exists.
func (u *UserService) GetUser(ctx context.Context, principal jwt.User, id string) (model.User, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "user_service_get_user")
	defer span.Finish()

	err := u.AuthService.AssertUserAccess(ctx, principal, id)
	if err != nil {
		return model.User{}, err
	}

	user, found, err := u.UserRepo.Find(ctx, id)
	if err != nil {
		return model.User{}, err
	}

	if !found {
		err = fmt.Errorf("user with id %s does not exist", id)
		return model.User{}, httputil.NotFoundError(err)
	}

	u.AuditLog.Read(ctx, principal.ID, "user:%s", id)
	return user, nil
}
