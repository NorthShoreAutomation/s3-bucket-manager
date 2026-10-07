package model

import "time"

type Bucket struct {
	Name         string
	Region       string
	CreationDate time.Time
	IsPublic     bool
	AccessKnown  bool
	ObjectCount  int64
}

type PermissionLevel string

const (
	PermRead            PermissionLevel = "read"
	PermReadWrite       PermissionLevel = "read-write"
	PermReadWriteDelete PermissionLevel = "read-write-delete"
)

type BucketAccess struct {
	Bucket     string
	Permission PermissionLevel
}

type UserPermission struct {
	Username   string
	Permission PermissionLevel
}

type User struct {
	Name          string
	ARN           string
	CreateDate    time.Time
	BucketAccess  []BucketAccess
	KeyCount      int
	KeyCountKnown bool
}

type AccessKey struct {
	AccessKeyID     string
	SecretAccessKey string
	UserName        string
	CreateDate      time.Time
	Status          string
}

type PrefixAccess struct {
	Prefix   string
	IsPublic bool
}
