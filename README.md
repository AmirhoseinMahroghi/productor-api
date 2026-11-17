# Productor API

بک‌اند مستقل Productor با **Go و PostgreSQL**. API در مسیر `/api/v1` پیاده‌سازی شده و رابط HTTP با کتابخانهٔ استاندارد Go و اتصال PostgreSQL با pgx کار می‌کند.

## امکانات پیاده‌سازی‌شده

- ورود با شمارهٔ موبایل و OTP توسعه‌ای، نشست تصادفی در کوکی HttpOnly و خروج از حساب
- ایجاد و ویرایش ارگان و کنترل دسترسی نقش‌محور
- ثبت محصول به‌شکل پیش‌نویس، ارسال برای بررسی، ویرایش نسخهٔ تازه و حفظ نسخهٔ منتشرشده تا تصمیم ناظر
- صف نظارت محصولات: انتشار، برگشت با دلیل، رد با دلیل، ویرایش ناظر و خروج از انتشار
- کاتالوگ عمومی، جست‌وجو، فیلتر، دسته‌ها، تاپیک‌ها و لیدربورد روزانه/ماهانه بر اساس ساعت تهران
- رأی مثبت یکتا برای هر حساب و امکان پس‌گرفتن آن
- ارسال نظر در صف انسانی؛ دلیل رد و مهلت ۴۸ ساعتهٔ ویرایش؛ حذف خودکار پس از پایان مهلت
- بارگذاری تصویر JPEG، PNG و WebP تا ۱۰ مگابایت با دسترسی خصوصی تا زمان انتشار
- اعلان‌های کاربر، مدیریت کاربران و گزارش ممیزی
- نقش‌های کارمندی قابل تعریف با مجوزهای جزئی و کنترل سمت سرور برای پنل و مسیرهای نظارت
- migrationهای PostgreSQL با قفل هم‌زمانی و endpointهای سلامت

## نیازمندی‌ها

- Go نسخهٔ ۱.۲۵ یا بالاتر
- PostgreSQL نسخهٔ ۱۳ یا بالاتر
- PowerShell در ویندوز (برای نمونه‌های زیر)

## ساخت پایگاه‌دادهٔ محلی

در PostgreSQL یک نقش و پایگاه‌داده بسازید. رمز را فقط در محیط خودتان تعیین کنید:

```sql
CREATE ROLE productor LOGIN PASSWORD 'یک-رمز-محلی-قوی';
CREATE DATABASE productor OWNER productor;
```

سپس در PowerShell، از پوشهٔ `E:\productor-api`:

```powershell
Copy-Item .env.example .env
notepad .env
. .\scripts\load-env.ps1
go run .\cmd\migrate
go run .\cmd\api
```

در `.env`، دست‌کم `DATABASE_URL` را با رمز محلی PostgreSQL خودتان تنظیم کنید. نمونهٔ قالب:

```text
DATABASE_URL=postgres://productor:YOUR_PASSWORD@localhost:5432/productor?sslmode=disable
```

اگر رمز شامل نویسه‌های ویژه است، آن را برای URL کدگذاری کنید. در محیط توسعه، اجرای `cmd/api` نیز migrationها را خودکار اعمال می‌کند؛ اجرای `cmd/migrate` جداگانه اختیاری است.

- سلامت HTTP: [http://localhost:8080/healthz](http://localhost:8080/healthz)
- آمادگی همراه با بررسی پایگاه‌داده: [http://localhost:8080/readyz](http://localhost:8080/readyz)
- اطلاعات API: [http://localhost:8080/api/v1](http://localhost:8080/api/v1)
- قرارداد OpenAPI: [openapi/openapi.yaml](openapi/openapi.yaml)

## OTP توسعه‌ای و مدیر اولیه

`OTP_MODE=development` حالت فعلی ورود است. کد OTP در پاسخ API ارسال نمی‌شود؛ فقط در ترمینالی که سرور را اجرا کرده‌اید، به‌صورت لاگ توسعه‌ای نمایش داده می‌شود و ۵ دقیقه اعتبار دارد. ارسال پیامک واقعی هنوز به درگاه پیامکی متصل نشده است.

برای مدیر اولیه، پیش از اجرای سرور شماره را در `BOOTSTRAP_ADMIN_PHONES` قرار دهید؛ پس از ورود همان شماره، نقش مدیر به حساب اختصاص می‌یابد. شماره با قالب بین‌المللی مانند `+98912...` یا شمارهٔ موبایل ایران قابل‌خواندن توسط سیستم است. شمارهٔ مدیر را در مخزن ثبت نکنید.

## تنظیمات

| متغیر | پیش‌فرض توسعه | کاربرد |
|---|---|---|
| `APP_ENV` | `development` | مقدارهای مجاز: `development`، `test`، `production` |
| `PORT` | `8080` | پورت HTTP |
| `DATABASE_URL` | خالی | نشانی اتصال PostgreSQL؛ الزامی |
| `FRONTEND_ORIGINS` | `http://localhost:3000,http://localhost:3002` | مبدأهای مجاز، جداشده با ویرگول |
| `PUBLIC_API_URL` | `http://localhost:8080` | ریشهٔ عمومی API برای نشانی فایل‌های بارگذاری‌شده |
| `UPLOAD_DIR` | `uploads` | محل فایل‌های رسانه؛ در استقرار پایدار نگه‌داری شود |
| `OTP_MODE` | `development` | فعلاً فقط حالت توسعه پشتیبانی می‌شود |
| `OTP_HASH_SECRET` | تولید خودکار در توسعه | راز HMAC کد OTP؛ در استقرار ثابت و محرمانه تنظیم شود |
| `BOOTSTRAP_ADMIN_PHONES` | خالی | شماره‌های مدیر اولیه، جداشده با ویرگول |
| `SESSION_COOKIE_NAME` | `productor_session` | نام کوکی نشست |
| `SESSION_COOKIE_DOMAIN` | خالی | دامنهٔ کوکی، در صورت نیاز |
| `COOKIE_SECURE` | وابسته به محیط | در تولید باید `true` باشد |

فایل `.env` فقط با اجرای `. .\scripts\load-env.ps1` در PowerShell بارگذاری می‌شود؛ خود برنامه فایل را به‌طور خودکار نمی‌خواند.

## قرارداد API

همهٔ پاسخ‌های خطا با قالب زیر برمی‌گردند:

```json
{"error":{"code":"invalid_request","message":"توضیح فارسی"}}
```

فهرست کامل مسیرها در OpenAPI است. مسیرهای اصلی:

- عمومی: `GET /products`، `GET /products/{slug}`، `GET /organizations`، `GET /topics`، `GET /categories`، `GET /leaderboard?period=today|month`
- ورود: `POST /auth/otp/request`، `POST /auth/otp/verify`، `POST /auth/logout`، `GET /me`
- ارگان و محصول: `POST /organizations`، `GET /me/organizations`، `POST /organizations/{slug}/products`، `GET /me/products`، `PATCH /products/{slug}`، `POST /products/{slug}/submit`
- مشارکت: `PUT/DELETE /products/{slug}/vote`، `GET/POST /products/{slug}/comments`، `GET /me/notifications`
- نظارت: `GET /moderation/products`، `POST /moderation/products/{slug}/decision`، `GET /moderation/comments`
- پنل مدیریت: GET /admin/overview، GET /admin/products، GET /admin/comments، GET/PATCH /admin/organizations، GET/POST/PATCH /admin/categories، GET/POST/PATCH/DELETE /admin/topics، GET/PATCH /admin/users، GET/POST/PATCH/DELETE /admin/roles، GET /admin/audit
- مجوزهای ارگان و طبقه‌بندی جداگانه تعریف شده‌اند؛ حذف موضوع به‌صورت غیرفعال‌سازی نرم انجام می‌شود و برچسب استفاده‌شده در محصول قابل حذف نیست.

نشست با کوکی HttpOnly نگه‌داری می‌شود. مرورگر فرانت‌اند باید درخواست‌های API را با credentials بفرستد. برای هر تغییر وضعیت مبتنی بر کوکی، مبدأ درخواست باید در `FRONTEND_ORIGINS` باشد.

## چرخهٔ انتشار محصول

هر تغییر محتوایی نسخهٔ تازه می‌سازد. اگر محصول قبلاً منتشر شده باشد، نسخهٔ عمومی قبلی تا تأیید نسخهٔ تازه نمایش داده می‌شود. برگشت یا رد محصول نیازمند دلیل ناظر است و در تاریخچه ذخیره می‌شود. ارگان پس از ساخت بلافاصله عمومی است، ولی محصول پیش از تأیید در کاتالوگ عمومی قرار نمی‌گیرد.

نظرهای تازه ابتدا `pending` هستند. در صورت رد، نویسنده تا ۴۸ ساعت می‌تواند متن را اصلاح و دوباره بفرستد. پاک‌سازی پس‌زمینه هر ۵ دقیقه اجرا می‌شود.

## ساخت

```powershell
go build ./...
go build -o .\bin\productor-api.exe .\cmd\api
```

## مرزهای فعلی

- فرانت‌اند جداگانه در مخزن `E:\Productor` قرار دارد و از طریق مسیر هم‌دامنهٔ Next.js (`/api/v1`) به این API متصل است؛ برای تنظیم اجرای یکپارچه، راهنمای `DEPLOYMENT.md` در مخزن فرانت‌اند را ببینید.
- OTP واقعی، moderation خودکار AI، مدیریت اعضای چندنفرهٔ ارگان و فضای ذخیره‌سازی ابری هنوز پیاده‌سازی نشده‌اند.
- رسانه‌ها روی دیسک محلی ذخیره می‌شوند؛ در استقرار باید پوشهٔ `UPLOAD_DIR` پایدار و پشتیبان‌گیری شود.
- OTP فقط در حالت توسعه فعال است و کد آن در لاگ API دیده می‌شود. سرور عمداً با `APP_ENV=production` بالا نمی‌آید تا درگاه پیامکی واقعی پیاده‌سازی و پیکربندی شود. پیش از انتشار عمومی، ارسال و محدودسازی OTP، راز ثابت و محرمانهٔ `OTP_HASH_SECRET`، `COOKIE_SECURE=true` و migration مستقل را آماده کنید.
