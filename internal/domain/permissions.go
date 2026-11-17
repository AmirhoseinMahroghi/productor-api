package domain

type PermissionDefinition struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"`
}

var AdminPermissions = []PermissionDefinition{
	{Key: "dashboard.view", Label: "داشبورد", Description: "مشاهدهٔ آمار و وضعیت کلی سامانه", Group: "dashboard"},
	{Key: "products.view", Label: "مشاهدهٔ صف محصولات", Description: "دیدن محصولات منتظر بررسی و تاریخچهٔ نسخه‌ها", Group: "products"},
	{Key: "products.review", Label: "تصمیم‌گیری دربارهٔ محصول", Description: "گرفتن صف و تأیید، برگشت یا رد محصول", Group: "products"},
	{Key: "products.edit", Label: "ویرایش محتوای محصول", Description: "ثبت اصلاح محتوایی توسط ناظر", Group: "products"},
	{Key: "products.unpublish", Label: "خروج محصول از انتشار", Description: "برداشتن محصول منتشرشده از کاتالوگ", Group: "products"},
	{Key: "comments.view", Label: "مشاهدهٔ نظرها", Description: "دیدن صف و نظرهای نیازمند بررسی", Group: "comments"},
	{Key: "comments.moderate", Label: "بررسی نظرها", Description: "تأیید یا رد نظرها با ثبت دلیل", Group: "comments"},
	{Key: "users.view", Label: "مشاهدهٔ کاربران", Description: "دیدن فهرست حساب‌ها و نقش جاری آن‌ها", Group: "users"},
	{Key: "users.manage", Label: "مدیریت وضعیت کاربران", Description: "مسدود یا آزاد کردن حساب‌ها و نشست‌هایشان", Group: "users"},
	{Key: "audit.view", Label: "مشاهدهٔ گزارش ممیزی", Description: "دیدن تغییرات مدیریتی ثبت‌شده", Group: "audit"},
	{Key: "organizations.view", Label: "مشاهدهٔ ارگان‌ها", Description: "دیدن فهرست ارگان‌ها و وضعیت محصولات آن‌ها", Group: "organizations"},
	{Key: "organizations.manage", Label: "مدیریت ارگان‌ها", Description: "ویرایش مشخصات عمومی ارگان‌ها", Group: "organizations"},
	{Key: "taxonomy.view", Label: "مشاهدهٔ دسته‌بندی‌ها و موضوع‌ها", Description: "دیدن دسته‌بندی‌ها و موضوع‌های کاتالوگ", Group: "taxonomy"},
	{Key: "taxonomy.manage", Label: "مدیریت دسته‌بندی‌ها و موضوع‌ها", Description: "ساخت و ویرایش دسته‌بندی‌ها و موضوع‌های کاتالوگ", Group: "taxonomy"},
}

func HasPermission(user User, permission string) bool {
	if user.Role == RoleAdmin {
		return true
	}
	for _, granted := range user.Permissions {
		if granted == permission {
			return true
		}
		switch permission {
		case "products.view":
			if granted == "products.review" || granted == "products.edit" || granted == "products.unpublish" {
				return true
			}
		case "comments.view":
			if granted == "comments.moderate" {
				return true
			}
		case "users.view":
			if granted == "users.manage" {
				return true
			}
		case "organizations.view":
			if granted == "organizations.manage" {
				return true
			}
		case "taxonomy.view":
			if granted == "taxonomy.manage" {
				return true
			}
		}
	}
	return false
}

func ValidAdminPermission(permission string) bool {
	for _, definition := range AdminPermissions {
		if definition.Key == permission {
			return true
		}
	}
	return false
}
