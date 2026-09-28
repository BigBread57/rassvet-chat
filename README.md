# Рассвет

Android-клиент и Go-сервис для комнатного чата в сети с двумя узлами. Текущий проверяемый этап: активация устройств, текст, вложения и обмен между двумя сервисами. Полные требования, включая голос и видео, описаны в [docs/requirements.md](docs/requirements.md); они не означают, что эти функции уже реализованы.

## Что установить для локальной разработки

- **Android Studio** с Android SDK Platform 37 и Build Tools для сборки клиента. Проект использует Android Gradle Plugin 9.1.1, Kotlin Compose 2.4.10 и Gradle 9.3.1 через включённый wrapper. Настройте JDK, совместимый с указанным Android Gradle Plugin, в Android Studio или через `JAVA_HOME`.
- **Go 1.22 или новее** и C-компилятор: драйвер SQLite в `service/go.mod` использует CGO. Для Windows-стенда ниже Go и компилятор нужны внутри WSL.
- Для проверки с телефона на **Windows**: WSL с Linux, Bash и Python 3 в Windows. `local-relay.py` использует только стандартную библиотеку Python. Компьютер и телефон должны быть в одной Wi-Fi сети.
- Для установки по USB: Android SDK Platform Tools (`adb`); можно также скопировать APK на телефон вручную.

## Проверка и сборка

Команды для сервиса выполняются из каталога `service` в Linux или WSL:

```sh
cd service
go test ./...
go build -o rassvetd ./cmd/rassvetd
```

Android-приложение можно открыть в Android Studio как проект из каталога `android`. Для сборки из терминала перейдите в `android` и выполните `./gradlew :app:assembleDebug` в Linux/macOS или `.\gradlew.bat :app:assembleDebug` в PowerShell. Укажите путь к Android SDK через `ANDROID_HOME` или локальный файл `android/local.properties` с `sdk.dir=<путь к SDK>`. APK появится в `android/app/build/outputs/apk/debug/app-debug.apk`. Gradle wrapper скачает Gradle и зависимости при первом запуске.

## Локальный стенд на Windows

1. Узнайте IPv4-адрес Wi-Fi адаптера командой `ipconfig` в PowerShell. Ниже `<PC_IP>` означает этот адрес без угловых скобок.
2. В WSL из каталога `service` запустите `bash local-stand.sh run <PC_IP>`. Скрипт соберёт Go-сервис и запустит два узла на портах 18443 и 18444. Путь к `admin-ticket.json` он выведет в терминал.
3. В PowerShell из того же каталога запустите `python local-relay.py <PC_IP>`. Оставьте оба терминала открытыми.
4. Установите собранный APK на Android-устройство, подключённое к той же Wi-Fi сети. Для активации администратора вставьте содержимое `admin-ticket.json` в экран ручной настройки. Код действует 10 минут; до активации новый код выдаёт `bash local-stand.sh ticket <PC_IP>` в WSL.

Если телефон не видит узлы, проверьте изоляцию клиентов Wi-Fi и входящие соединения Windows на портах 18443/18444. Для переписки между пользователями нужен второй телефон или эмулятор. Подробные команды и сценарии: [локальный запуск сервиса](docs/service-local.md), [полевой тест](docs/field-test-guide.md), [контракт API](docs/api.md).

Локальный стенд не подтверждает работу на OpenMANET или радиоканале. Установка и автозапуск сервиса на целевых Raspberry Pi пока не проверены.
