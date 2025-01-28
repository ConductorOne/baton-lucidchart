package client

import (
	"fmt"
	"strconv"
	"time"
)

type User struct {
	AccountId int      `json:"accountId"`
	Email     string   `json:"email"`
	Name      string   `json:"name"`
	UserId    int      `json:"userId"`
	Usernames string   `json:"usernames"`
	Roles     []string `json:"roles"`
}

type Folder struct {
	Id      int        `json:"id"`
	Type    string     `json:"type"`
	Name    string     `json:"name"`
	Parent  int        `json:"parent"`
	Created time.Time  `json:"created"`
	Trashed *time.Time `json:"trashed"`
}

type FolderContent struct {
	Id       interface{} `json:"id"`
	Type     string      `json:"type"`
	Name     string      `json:"name"`
	Shortcut bool        `json:"shortcut"`
	Product  string      `json:"product"`
}

func (f *FolderContent) ID() string {
	switch v := f.Id.(type) {
	case int:
		return strconv.Itoa(v)
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

type AccountDocument struct {
	DocumentId   string    `json:"documentId"`
	Title        string    `json:"title"`
	AdminViewUrl string    `json:"adminViewUrl"`
	Created      time.Time `json:"created"`
	Owner        struct {
		OwnerType string `json:"ownerType"`
		Id        string `json:"id"`
	} `json:"owner"`
	LastModified time.Time  `json:"lastModified"`
	CustomTags   []string   `json:"customTags"`
	Product      string     `json:"product"`
	Status       string     `json:"status"`
	Parent       int        `json:"parent"`
	Trashed      *time.Time `json:"trashed"`
}

type DocumentUserCollaboration struct {
	DocumentId string    `json:"documentId"`
	UserId     int       `json:"userId"`
	Role       string    `json:"role"`
	Created    time.Time `json:"created"`
}

type FolderUserCollaboration struct {
	FolderId int       `json:"folderId"`
	UserId   int       `json:"userId"`
	Role     string    `json:"role"`
	Created  time.Time `json:"created"`
}

type FolderGroupCollaborator struct {
	FolderId int       `json:"folderId"`
	GroupId  int       `json:"groupId"`
	Role     string    `json:"role"`
	Created  time.Time `json:"created"`
}

type DocumentShareLink struct {
	ShareLinkId  string `json:"shareLinkId"`
	DocumentId   string `json:"documentId"`
	Role         string `json:"role"`
	LinkSecurity struct {
		RestrictToAccount bool      `json:"restrictToAccount"`
		Expires           time.Time `json:"expires"`
		Passcode          string    `json:"passcode"`
		AllowAnonymous    bool      `json:"allowAnonymous"`
	} `json:"linkSecurity"`
	Created      time.Time `json:"created"`
	CreatedBy    int       `json:"createdBy"`
	LastModified time.Time `json:"lastModified"`
	AcceptUrl    string    `json:"acceptUrl"`
}
