package types

type Config struct {
	Superusers []User
}

type User struct {
	Username       string
	PasswordDigest string
}

type Post struct {
}
