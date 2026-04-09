package model

import "time"

type Bucket struct {
	Name         string
	Region       string
	CreationDate time.Time
	IsPublic     bool
	ObjectCount  int64
}

type User struct {
	Name       string
	ARN        string
	CreateDate time.Time
	Buckets    []string
	KeyCount   int
}

type AccessKey struct {
	AccessKeyID     string
	SecretAccessKey  string
	UserName        string
	CreateDate      time.Time
}

type PrefixAccess struct {
	Prefix   string
	IsPublic bool
}
