package shared

type User struct {
	ID    int64 `agg:"auto"`
	Name  string
	Email string
	Age   int
}

type Order struct {
	ID       int64 `agg:"auto"`
	Customer string
	Total    float64
	Items    []*OrderItem
	Notes    []*OrderNote
}

type OrderItem struct {
	ID       int64 `agg:"auto"`
	OrderID  int64 `agg:"parental"`
	Product  string
	Quantity int
	Price    float64
}

type OrderNote struct {
	ID      int64 `agg:"auto"`
	OrderID int64 `agg:"parental"`
	Content string
}
