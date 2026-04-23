package shared

type User struct {
	ID    int64 `ormapper:"auto"`
	Name  string
	Email string
	Age   int
}

type Order struct {
	ID       int64 `ormapper:"auto"`
	Customer string
	Total    float64
	Items    []*OrderItem
	Notes    []*OrderNote
}

type OrderItem struct {
	ID       int64 `ormapper:"auto"`
	OrderID  int64 `ormapper:"parental"`
	Product  string
	Quantity int
	Price    float64
}

type OrderNote struct {
	ID      int64 `ormapper:"auto"`
	OrderID int64 `ormapper:"parental"`
	Content string
}
