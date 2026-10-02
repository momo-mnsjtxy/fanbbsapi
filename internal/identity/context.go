package identity

import "context"

func ContextWithUser(ctx context.Context, user User, sessionID string) context.Context {
	ctx = context.WithValue(ctx, currentUserKey{}, user)
	return context.WithValue(ctx, currentSessionKey{}, sessionID)
}

func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(currentUserKey{}).(User)
	return user, ok
}

func SessionFromContext(ctx context.Context) (string, bool) {
	session, ok := ctx.Value(currentSessionKey{}).(string)
	return session, ok
}
