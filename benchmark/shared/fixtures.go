package shared

func NewUser() *User {
	return &User{
		Name:  "Alice",
		Email: "alice@example.com",
		Age:   30,
	}
}

func NewOrder() *Order {
	return &Order{
		Customer: "John Doe",
		Total:    299.97,
		Items: []*OrderItem{
			{Product: "Product A", Quantity: 1, Price: 99.99},
			{Product: "Product B", Quantity: 1, Price: 99.99},
			{Product: "Product C", Quantity: 1, Price: 99.99},
		},
		Notes: []*OrderNote{
			{Content: "Please deliver before 5pm"},
			{Content: "Gift wrap requested"},
		},
	}
}

func MutateOrder(order *Order) {
	order.Total = 399.97
	if len(order.Items) > 0 {
		order.Items[0].Quantity++
	}
	if len(order.Items) > 1 {
		order.Items = order.Items[:len(order.Items)-1]
	}
	order.Items = append(order.Items, &OrderItem{
		Product:  "Product D",
		Quantity: 1,
		Price:    99.99,
	})
	if len(order.Notes) > 0 {
		order.Notes[0].Content = "Updated delivery note"
	}
}
