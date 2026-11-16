package orders

// Read returns the order of its owner, with its status.
func Read(id string) (int, Order) {
	return 200, find(id)
}
