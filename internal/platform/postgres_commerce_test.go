package platform_test

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"fanbbs.local/backend/internal/commerce"
	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func TestPostgresNonPaymentCommerceConcurrency(t *testing.T) {
	dsn := os.Getenv("FANBBS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FANBBS_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	resetPostgres(t, ctx, dsn)
	db, err := platform.OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ids := identity.NewService(db)
	admin, err := ids.Register(ctx, identity.RegisterInput{Handle: "shop_admin", Email: "shop-admin@example.test", Password: "synthetic test 123", DisplayName: "Shop Admin"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := ids.Register(ctx, identity.RegisterInput{Handle: "shop_member", Email: "shop-member@example.test", Password: "synthetic test 123", DisplayName: "Shop Member"})
	if err != nil {
		t.Fatal(err)
	}
	service := commerce.NewService(db)
	group, err := service.CreateType(ctx, admin.User, "request-type", commerce.TypeInput{Slug: "local-test", Name: "Synthetic local products"})
	if err != nil {
		t.Fatal(err)
	}
	stock := 50
	product, err := service.CreateProduct(ctx, admin.User, "request-product", commerce.ProductInput{TypeID: group.ID, SKU: "local-item", Name: "Synthetic item", Inventory: &stock, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	address, err := service.CreateShippingAddress(ctx, member.User.ID, commerce.ShippingAddressInput{Label: "Synthetic", RecipientName: "Test User", Phone: "000-0000", Region: "Test Region", AddressLine: "Synthetic Address", Default: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCartItem(ctx, member.User.ID, product.ID, 2); err != nil {
		t.Fatal(err)
	}
	const contenders = 12
	start := make(chan struct{})
	var wait sync.WaitGroup
	orderIDs := make(chan string, contenders)
	errors := make(chan error, contenders)
	for i := 0; i < contenders; i++ {
		wait.Go(func() {
			<-start
			order, _, err := service.CreateOrderWithAddress(ctx, member.User.ID, "same-checkout", address.ID)
			orderIDs <- order.ID
			errors <- err
		})
	}
	close(start)
	wait.Wait()
	close(orderIDs)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Errorf("concurrent checkout: %v", err)
		}
	}
	firstID := ""
	for id := range orderIDs {
		if firstID == "" {
			firstID = id
		}
		if id != firstID {
			t.Errorf("checkout replay returned different order: %s != %s", id, firstID)
		}
	}
	current, err := service.Product(ctx, product.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if current.Inventory != 48 {
		t.Errorf("stock reserved more than once: %d", current.Inventory)
	}
	if t.Failed() {
		return
	}
	start = make(chan struct{})
	errors = make(chan error, contenders)
	for i := 0; i < contenders; i++ {
		wait.Go(func() {
			<-start
			_, _, err := service.TransitionOrder(ctx, member.User, "cancel", firstID, commerce.OrderTransitionInput{Status: "cancelled", Reason: "Synthetic cancellation"}, false)
			errors <- err
		})
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Errorf("concurrent cancellation: %v", err)
		}
	}
	current, err = service.Product(ctx, product.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if current.Inventory != 50 {
		t.Errorf("stock restored more than once: %d", current.Inventory)
	}
	start = make(chan struct{})
	errors = make(chan error, contenders)
	for i := 0; i < contenders; i++ {
		wait.Go(func() { <-start; _, _, err := service.CheckIn(ctx, member.User.ID); errors <- err })
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Errorf("concurrent check-in: %v", err)
		}
	}
	status, err := service.GamificationStatus(ctx, member.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Balance != 5 {
		t.Errorf("check-in awarded wrong balance: %d", status.Balance)
	}
	event, _, err := service.GrantPoints(ctx, admin.User, "grant", member.User.ID, "manual-once", "Synthetic noncash grant", 7)
	if err != nil {
		t.Fatal(fmt.Errorf("grant points after check-in: %w", err))
	}
	if event.Amount != 7 {
		t.Fatal("incorrect point event")
	}
	t.Run("concurrent-default-addresses", func(t *testing.T) {
		runConcurrent(t, 8, func(i int) error {
			_, err := service.CreateShippingAddress(ctx, member.User.ID, commerce.ShippingAddressInput{Label: fmt.Sprintf("Address %d", i), RecipientName: "Test User", Phone: "000-0000", Region: "Test Region", AddressLine: "Synthetic New Address", Default: true})
			return err
		})
		var defaults, total int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(is_default),0) FROM shipping_addresses WHERE user_id=?`, member.User.ID).Scan(&total, &defaults); err != nil {
			t.Fatal(err)
		}
		if defaults != 1 || total != 9 {
			t.Fatalf("default address invariant: total=%d default=%d", total, defaults)
		}
		order, err := service.Order(ctx, firstID, member.User.ID, false)
		if err != nil || order.ShippingAddress == nil || order.ShippingAddress.AddressLine != "Synthetic Address" {
			t.Fatalf("order snapshot changed: %#v %v", order, err)
		}
	})
	t.Run("concurrent-task-awards", func(t *testing.T) {
		task, err := service.CreateTask(ctx, admin.User, "create-task", commerce.TaskInput{Code: "verified-once", Name: "Verified synthetic task", RepeatPolicy: "once", RewardPoints: 11, Status: "active"})
		if err != nil {
			t.Fatal(err)
		}
		runConcurrent(t, 12, func(i int) error {
			_, _, err := service.AwardTask(ctx, admin.User, fmt.Sprintf("award-%d", i), member.User.ID, task.ID, fmt.Sprintf("evidence-%d", i), "Synthetic verified award")
			return err
		})
		status, err := service.GamificationStatus(ctx, member.User.ID)
		if err != nil || status.Balance != 23 {
			t.Fatalf("points awarded multiple times: %#v %v", status, err)
		}
		var claims, audits int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_claims WHERE user_id=?`, member.User.ID).Scan(&claims); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='task_reward_award'`).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if claims != 1 || audits != 1 {
			t.Fatalf("duplicate task facts: claims=%d audits=%d", claims, audits)
		}
	})
	t.Run("different-checkout-keys-consume-cart-once", func(t *testing.T) {
		if _, err := service.SetCartItem(ctx, member.User.ID, product.ID, 1); err != nil {
			t.Fatal(err)
		}
		var succeeded int
		var lock sync.Mutex
		runConcurrent(t, 12, func(i int) error {
			_, _, err := service.CreateOrder(ctx, member.User.ID, fmt.Sprintf("different-checkout-%d", i))
			if err == nil {
				lock.Lock()
				succeeded++
				lock.Unlock()
				return nil
			}
			var problem *platform.AppError
			if stderrors.As(err, &problem) && problem.Code == "empty_cart" {
				return nil
			}
			return err
		})
		if succeeded != 1 {
			t.Fatalf("same cart checked out %d times", succeeded)
		}
		product, err := service.Product(ctx, product.ID, true)
		if err != nil || product.Inventory != 49 {
			t.Fatalf("cart stock mismatch: %#v %v", product, err)
		}
	})
	t.Run("audit-failure-rolls-back-cancellation", func(t *testing.T) {
		if _, err := service.SetCartItem(ctx, member.User.ID, product.ID, 1); err != nil {
			t.Fatal(err)
		}
		order, _, err := service.CreateOrder(ctx, member.User.ID, "audit-failure-order")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_commerce_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced audit failure'; END $$; CREATE TRIGGER reject_commerce_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_commerce_audit()`); err != nil {
			t.Fatal(err)
		}
		_, _, err = service.TransitionOrder(ctx, member.User, "audit-failure", order.ID, commerce.OrderTransitionInput{Status: "cancelled", Reason: "Must roll back"}, false)
		if err == nil {
			t.Fatal("forced audit failure unexpectedly succeeded")
		}
		if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_commerce_audit ON audit_events; DROP FUNCTION reject_commerce_audit()`); err != nil {
			t.Fatal(err)
		}
		after, err := service.Order(ctx, order.ID, member.User.ID, false)
		if err != nil || after.Status != "created" {
			t.Fatalf("order escaped rollback: %#v %v", after, err)
		}
		current, err := service.Product(ctx, product.ID, true)
		if err != nil || current.Inventory != 48 {
			t.Fatalf("inventory escaped rollback: %#v %v", current, err)
		}
		if _, _, err := service.TransitionOrder(ctx, member.User, "retry", order.ID, commerce.OrderTransitionInput{Status: "cancelled", Reason: "Retry after removed failure"}, false); err != nil {
			t.Fatal(err)
		}
	})

}

func runConcurrent(t *testing.T, n int, work func(int) error) {
	t.Helper()
	var wait sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wait.Go(func() { <-start; results <- work(i) })
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}
