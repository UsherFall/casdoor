// Copyright 2021 The Casdoor Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package object

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/casdoor/casdoor/cred"
	"github.com/casdoor/casdoor/form"
	"github.com/casdoor/casdoor/i18n"
	"github.com/casdoor/casdoor/util"
	goldap "github.com/go-ldap/ldap/v3"
)

const (
	SigninWrongTimesLimit     = 5
	LastSignWrongTimeDuration = time.Minute * 15
)

func CheckUserSignup(application *Application, organization *Organization, form *form.AuthForm, lang string) string {
	if organization == nil {
		return i18n.Translate(lang, "check:Organization does not exist")
	}

	if application.IsSignupItemVisible("Username") {
		if len(form.Username) <= 1 {
			return i18n.Translate(lang, "check:Username must have at least 2 characters")
		}
		if unicode.IsDigit(rune(form.Username[0])) {
			return i18n.Translate(lang, "check:Username cannot start with a digit")
		}
		if util.IsEmailValid(form.Username) {
			return i18n.Translate(lang, "check:Username cannot be an email address")
		}
		if util.ReWhiteSpace.MatchString(form.Username) {
			return i18n.Translate(lang, "check:Username cannot contain white spaces")
		}

		if msg := CheckUsername(form.Username, lang); msg != "" {
			return msg
		}

		if HasUserByField(organization.Name, "name", form.Username) {
			return i18n.Translate(lang, "check:Username already exists")
		}
		if HasUserByField(organization.Name, "email", form.Email) {
			return i18n.Translate(lang, "check:Email already exists")
		}
		if HasUserByField(organization.Name, "phone", form.Phone) {
			return i18n.Translate(lang, "check:Phone already exists")
		}
	}

	if application.IsSignupItemVisible("Password") {
		msg := CheckPasswordComplexityByOrg(organization, form.Password)
		if msg != "" {
			return msg
		}
	}

	if application.IsSignupItemVisible("Email") {
		if form.Email == "" {
			if application.IsSignupItemRequired("Email") {
				return i18n.Translate(lang, "check:Email cannot be empty")
			}
		} else {
			if HasUserByField(organization.Name, "email", form.Email) {
				return i18n.Translate(lang, "check:Email already exists")
			} else if !util.IsEmailValid(form.Email) {
				return i18n.Translate(lang, "check:Email is invalid")
			}
		}
	}

	if application.IsSignupItemVisible("Phone") {
		if form.Phone == "" {
			if application.IsSignupItemRequired("Phone") {
				return i18n.Translate(lang, "check:Phone cannot be empty")
			}
		} else {
			if HasUserByField(organization.Name, "phone", form.Phone) {
				return i18n.Translate(lang, "check:Phone already exists")
			} else if !util.IsPhoneAllowInRegin(form.CountryCode, organization.CountryCodes) {
				return i18n.Translate(lang, "check:Your region is not allow to signup by phone")
			} else if !util.IsPhoneValid(form.Phone, form.CountryCode) {
				return i18n.Translate(lang, "check:Phone number is invalid")
			}
		}
	}

	if application.IsSignupItemVisible("Display name") {
		if application.GetSignupItemRule("Display name") == "First, last" && (form.FirstName != "" || form.LastName != "") {
			if form.FirstName == "" {
				return i18n.Translate(lang, "check:FirstName cannot be blank")
			} else if form.LastName == "" {
				return i18n.Translate(lang, "check:LastName cannot be blank")
			}
		} else {
			if form.Name == "" {
				return i18n.Translate(lang, "check:DisplayName cannot be blank")
			} else if application.GetSignupItemRule("Display name") == "Real name" {
				if !isValidRealName(form.Name) {
					return i18n.Translate(lang, "check:DisplayName is not valid real name")
				}
			}
		}
	}

	if application.IsSignupItemVisible("Affiliation") {
		if form.Affiliation == "" {
			return i18n.Translate(lang, "check:Affiliation cannot be blank")
		}
	}

	if len(application.InvitationCodes) > 0 {
		if form.InvitationCode == "" {
			if application.IsSignupItemRequired("Invitation code") {
				return i18n.Translate(lang, "check:Invitation code cannot be blank")
			}
		} else {
			if !util.InSlice(application.InvitationCodes, form.InvitationCode) {
				return i18n.Translate(lang, "check:Invitation code is invalid")
			}
		}
	}

	return ""
}

func checkSigninErrorTimes(user *User, lang string) string {
	if user.SigninWrongTimes >= SigninWrongTimesLimit {
		lastSignWrongTime, _ := time.Parse(time.RFC3339, user.LastSigninWrongTime)
		passedTime := time.Now().UTC().Sub(lastSignWrongTime)
		minutes := int(LastSignWrongTimeDuration.Minutes() - passedTime.Minutes())

		// deny the login if the error times is greater than the limit and the last login time is less than the duration
		if minutes > 0 {
			return fmt.Sprintf(i18n.Translate(lang, "check:You have entered the wrong password or code too many times, please wait for %d minutes and try again"), minutes)
		}

		// reset the error times
		user.SigninWrongTimes = 0

		UpdateUser(user.GetId(), user, []string{"signin_wrong_times"}, false)
	}

	return ""
}

func CheckPassword(user *User, password string, lang string, options ...bool) string {
	enableCaptcha := false
	if len(options) > 0 {
		enableCaptcha = options[0]
	}
	// check the login error times
	if !enableCaptcha {
		if msg := checkSigninErrorTimes(user, lang); msg != "" {
			return msg
		}
	}

	organization, err := GetOrganizationByUser(user)
	if err != nil {
		panic(err)
	}

	if organization == nil {
		return i18n.Translate(lang, "check:Organization does not exist")
	}

	passwordType := user.PasswordType
	if passwordType == "" {
		passwordType = organization.PasswordType
	}
	credManager := cred.GetCredManager(passwordType)
	if credManager != nil {
		if organization.MasterPassword != "" {
			if credManager.IsPasswordCorrect(password, organization.MasterPassword, "", organization.PasswordSalt) {
				resetUserSigninErrorTimes(user)
				return ""
			}
		}

		if credManager.IsPasswordCorrect(password, user.Password, user.PasswordSalt, organization.PasswordSalt) {
			resetUserSigninErrorTimes(user)
			return ""
		}

		return recordSigninErrorInfo(user, lang, enableCaptcha)
	} else {
		return fmt.Sprintf(i18n.Translate(lang, "check:unsupported password type: %s"), organization.PasswordType)
	}
}

func CheckPasswordComplexityByOrg(organization *Organization, password string) string {
	errorMsg := checkPasswordComplexity(password, organization.PasswordOptions)
	return errorMsg
}

func CheckPasswordComplexity(user *User, password string) string {
	organization, _ := GetOrganizationByUser(user)
	return CheckPasswordComplexityByOrg(organization, password)
}

func checkLdapUserPassword(user *User, password string, lang string) string {
	ldaps, err := GetLdaps(user.Owner)
	if err != nil {
		return err.Error()
	}

	ldapLoginSuccess := false
	hit := false

	for _, ldapServer := range ldaps {
		conn, err := ldapServer.GetLdapConn()
		if err != nil {
			continue
		}

		searchReq := goldap.NewSearchRequest(ldapServer.BaseDn, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
			0, 0, false, ldapServer.buildAuthFilterString(user), []string{}, nil)

		searchResult, err := conn.Conn.Search(searchReq)
		if err != nil {
			return err.Error()
		}

		if len(searchResult.Entries) == 0 {
			continue
		}
		if len(searchResult.Entries) > 1 {
			return i18n.Translate(lang, "check:Multiple accounts with same uid, please check your ldap server")
		}

		hit = true
		dn := searchResult.Entries[0].DN
		if err := conn.Conn.Bind(dn, password); err == nil {
			ldapLoginSuccess = true
			break
		}
	}

	if !ldapLoginSuccess {
		if !hit {
			return "user not exist"
		}
		return i18n.Translate(lang, "check:LDAP user name or password incorrect")
	}
	return ""
}

func CheckUserPassword(organization string, username string, password string, lang string, options ...bool) (*User, string) {
	enableCaptcha := false
	if len(options) > 0 {
		enableCaptcha = options[0]
	}
	user, err := GetUserByFields(organization, username)
	if err != nil {
		panic(err)
	}

	if user == nil || user.IsDeleted {
		return nil, fmt.Sprintf(i18n.Translate(lang, "general:The user: %s doesn't exist"), util.GetId(organization, username))
	}

	if user.IsForbidden {
		return nil, i18n.Translate(lang, "check:The user is forbidden to sign in, please contact the administrator")
	}

	if user.Ldap != "" {
		// ONLY for ldap users
		if msg := checkLdapUserPassword(user, password, lang); msg != "" {
			if msg == "user not exist" {
				return nil, fmt.Sprintf(i18n.Translate(lang, "check:The user: %s doesn't exist in LDAP server"), username)
			}
			return nil, msg
		}
	} else {
		if msg := CheckPassword(user, password, lang, enableCaptcha); msg != "" {
			return nil, msg
		}
	}
	return user, ""
}

func CheckUserPermission(requestUserId, userId string, strict bool, lang string) (bool, error) {
	if requestUserId == "" {
		return false, fmt.Errorf(i18n.Translate(lang, "general:Please login first"))
	}

	userOwner := util.GetOwnerFromId(userId)

	if userId != "" {
		targetUser, err := GetUser(userId)
		if err != nil {
			panic(err)
		}

		if targetUser == nil {
			if strings.HasPrefix(requestUserId, "built-in/") {
				return true, nil
			}

			return false, fmt.Errorf(i18n.Translate(lang, "general:The user: %s doesn't exist"), userId)
		}

		userOwner = targetUser.Owner
	}

	hasPermission := false
	if strings.HasPrefix(requestUserId, "app/") {
		hasPermission = true
	} else {
		requestUser, err := GetUser(requestUserId)
		if err != nil {
			return false, err
		}

		if requestUser == nil {
			return false, fmt.Errorf(i18n.Translate(lang, "check:Session outdated, please login again"))
		}
		if requestUser.IsGlobalAdmin() {
			hasPermission = true
		} else if requestUserId == userId {
			hasPermission = true
		} else if userOwner == requestUser.Owner {
			if strict {
				hasPermission = requestUser.IsAdmin
			} else {
				hasPermission = true
			}
		}
	}

	return hasPermission, fmt.Errorf(i18n.Translate(lang, "auth:Unauthorized operation"))
}

func CheckLoginPermission(userId string, application *Application) (bool, error) {
	var err error
	if userId == "built-in/admin" {
		return true, nil
	}

	permissions, err := GetPermissions(application.Organization)
	if err != nil {
		return false, err
	}

	allowPermissionCount := 0
	denyPermissionCount := 0
	allowCount := 0
	denyCount := 0
	for _, permission := range permissions {
		if !permission.IsEnabled || permission.ResourceType != "Application" || !permission.isResourceHit(application.Name) {
			continue
		}

		if !permission.isUserHit(userId) {
			if permission.Effect == "Allow" {
				allowPermissionCount += 1
			} else {
				denyPermissionCount += 1
			}
			continue
		}

		enforcer := getPermissionEnforcer(permission)

		var isAllowed bool
		isAllowed, err = enforcer.Enforce(userId, application.Name, "Read")
		if err != nil {
			return false, err
		}

		if isAllowed {
			if permission.Effect == "Allow" {
				allowCount += 1
			}
		} else {
			if permission.Effect == "Deny" {
				denyCount += 1
			}
		}
	}

	// Deny-override, if one deny is found, then deny
	if denyCount > 0 {
		return false, nil
	} else if allowCount > 0 {
		return true, nil
	}

	// For no-allow and no-deny condition
	// If only allow permissions exist, we suppose it's Deny-by-default, aka no-allow means deny
	// Otherwise, it's Allow-by-default, aka no-deny means allow
	if allowPermissionCount > 0 && denyPermissionCount == 0 {
		return false, nil
	}
	return true, nil
}

func CheckUsername(username string, lang string) string {
	if username == "" {
		return i18n.Translate(lang, "check:Empty username.")
	} else if len(username) > 39 {
		return i18n.Translate(lang, "check:Username is too long (maximum is 39 characters).")
	}

	// https://stackoverflow.com/questions/58726546/github-username-convention-using-regex

	if !util.ReUserName.MatchString(username) {
		return i18n.Translate(lang, "check:The username may only contain alphanumeric characters, underlines or hyphens, cannot have consecutive hyphens or underlines, and cannot begin or end with a hyphen or underline.")
	}

	return ""
}

func CheckUpdateUser(oldUser, user *User, lang string) string {
	if oldUser.Name != user.Name {
		if msg := CheckUsername(user.Name, lang); msg != "" {
			return msg
		}
		if HasUserByField(user.Owner, "name", user.Name) {
			return i18n.Translate(lang, "check:Username already exists")
		}
	}
	if oldUser.Email != user.Email {
		if HasUserByField(user.Owner, "email", user.Email) {
			return i18n.Translate(lang, "check:Email already exists")
		}
	}
	if oldUser.Phone != user.Phone {
		if HasUserByField(user.Owner, "phone", user.Phone) {
			return i18n.Translate(lang, "check:Phone already exists")
		}
	}

	return ""
}

func CheckToEnableCaptcha(application *Application, organization, username string) (bool, error) {
	if len(application.Providers) == 0 {
		return false, nil
	}

	for _, providerItem := range application.Providers {
		if providerItem.Provider == nil {
			continue
		}
		if providerItem.Provider.Category == "Captcha" {
			if providerItem.Rule == "Dynamic" {
				user, err := GetUserByFields(organization, username)
				if err != nil {
					return false, err
				}
				return user != nil && user.SigninWrongTimes >= SigninWrongTimesLimit, nil
			}
			return providerItem.Rule == "Always", nil
		}
	}

	return false, nil
}
