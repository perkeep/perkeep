# plaid-go

plaid-go is a Go client implementation of the [Plaid API](https://plaid.com/docs).

Install via `go get github.com/plaid/plaid-go`.

**Documentation:** [![GoDoc](https://godoc.org/github.com/plaid/plaid-go?status.svg)](https://godoc.org/github.com/plaid/plaid-go/plaid)

TODO:
- Complete README
- Complete testing
- Add CI


## Examples

### Adding an Auth user

```go
client := plaid.NewClient("test_id", "test_secret", plaid.Tartan)

// POST /auth
postRes, mfaRes, err := client.AuthAddUser("plaid_test", "plaid_good", "", "bofa", nil)
if err != nil {
    fmt.Println(err)
} else if mfaRes != nil {
    // Need to switch on different MFA types. See https://plaid.com/docs/api/#auth-mfa.
    switch mfaRes.Type {
    case "device":
        fmt.Println("--Device MFA--")
        fmt.Println("Message:", mfaRes.Device.Message)
    case "list":
        fmt.Println("--List MFA--")
        fmt.Println("Mask:", mfaRes.List[0].Mask, "\nType:", mfaRes.List[0].Type)
    case "questions":
        fmt.Println("--Questions MFA--")
        fmt.Println("Question:", mfaRes.Questions[0].Question)
    case "selections":
        fmt.Println("--Selections MFA--")
        fmt.Println("Question:", mfaRes.Selections[1].Question)
        fmt.Println("Answers:", mfaRes.Selections[1].Answers)
    }

    postRes2, mfaRes2, err := client.AuthStepSendMethod(mfaRes.AccessToken, "type", "email")
    if err != nil {
        fmt.Println("Error submitting send_method", err)
    }
    fmt.Println(mfaRes2, postRes2)

    postRes2, mfaRes2, err = client.AuthStep(mfaRes.AccessToken, "tomato")
    if err != nil {
        fmt.Println("Error submitting mfa", err)
    } else {
        fmt.Println(mfaRes2, postRes2)
    }
} else {
    fmt.Println(postRes.Accounts)
    fmt.Println("Auth Get")
    fmt.Println(client.AuthGet("test_bofa"))

    fmt.Println("Auth DELETE")
    fmt.Println(client.AuthDelete("test_bofa"))
}
```

### Plaid Link Exchange Token Process

Exchange a [Plaid Link][1] `public_token` for an API `access_token`:

```go
client := plaid.NewClient("test_id", "test_secret", plaid.Tartan)

// POST /exchange_token
postRes, err := client.ExchangeToken(public_token)
if err != nil {
    fmt.Println(err)
} else {
    // Use the returned Plaid API access_token to retrieve
    // account information.
    fmt.Println(postRes.AccessToken)
    fmt.Println("Auth Get")
    fmt.Println(client.AuthGet(postRes.AccessToken))
}
```

With the [Plaid + Stripe ACH integration][2], exchange a Link `public_token`
and `account_id` for an API `access_token` and Stripe `bank_account_token`:

```go
client := plaid.NewClient(CLIENT_ID, SECRET, plaid.Tartan)

// POST /exchange_token
postRes, err := client.ExchangeTokenAccount(public_token, account_id)
if err != nil {
    fmt.Println(err)
} else {
    // Use the returned Plaid access_token to make Plaid API requests and the
    // Stripe bank account token to make Stripe ACH API requests.
    fmt.Println(postRes.AccessToken)
    fmt.Println(postRes.BankAccountToken)
}
```

### Querying a category
```go
// GET /categories/13001001
category, err := plaid.GetCategory(plaid.Tartan, "13001001")
if err != nil {
    fmt.Println(err)
} else {
    fmt.Println("category", category.ID, "is", strings.Join(category.Hierarchy, ", "))
}
```

[1]: https://plaid.com/docs/link
[2]: https://plaid.com/docs/link/stripe
