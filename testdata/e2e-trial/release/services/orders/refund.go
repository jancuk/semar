package orders

// Refund refuses an amount above what is left of the order.
func Refund(o Order, amount int64) (int, error) {
	if amount > o.Amount-o.Refunded {
		return 422, errExceedsTotal
	}
	return 201, nil
}
