# dumper

Агент для VPS: снимает дамп Postgres из контейнера Docker и заливает архив в S3-совместимое хранилище. На каждой машине стоит свой бинарник. Расписание задаёт systemd timer: `dumper backup` отрабатывает один раз и завершается.

Для каждой цели из конфига агент запускает `pg_dump -Fc` внутри контейнера через Docker API. Версия `pg_dump` совпадает с сервером, клиент Postgres на хост ставить не нужно. Архив пишется во временный файл, проверяется заголовок `PGDMP`, затем файл уходит в S3. Поток напрямую в бакет не пишется: оборванная загрузка не оставляет битый объект.

Ключ объекта:

```text
{prefix}/{target}/{database}/{UTC timestamp}.dump
```

Пример: `vps-1/myapp/myapp/20261017T010203Z.dump`. Повторный запуск не перезаписывает предыдущий дамп.

Логи пишутся на английском в stderr. Пароли в них не попадают.

## Сборка

Нужен Go 1.24 или новее.

```sh
make build
```

Бинарник собирается в `dist/dumper` для Linux amd64. Для ARM-сервера: `make build ARCH=arm64`.

## Установка

На сервере, рядом со собранным бинарником:

```sh
sudo ./dumper install
```

С машины, где лежит репозиторий, то же самое на удалённый хост:

```sh
make deploy HOST=user@host
```

Команда копирует бинарник в `/usr/local/bin/dumper`, создаёт `/etc/dumper` и `/var/lib/dumper`, кладёт unit-файлы systemd и выполняет `systemctl daemon-reload`. Пример конфига записывается в `/etc/dumper/config.yaml` только если этого файла ещё нет. Повторный запуск обновляет бинарник и unit-файлы. Таймер команда не включает.

Отредактируйте `/etc/dumper/config.yaml`, затем:

```sh
sudo dumper check -config /etc/dumper/config.yaml
sudo systemctl enable --now dumper.timer
sudo systemctl start dumper.service
journalctl -u dumper.service
```

Timer по умолчанию запускает дамп каждый день в 03:30 по времени сервера. Расписание меняется полем `OnCalendar` в `dumper.timer`. `Persistent=true` догоняет пропущенный запуск после простоя машины.

Включайте timer, а не service: service — разовый запуск, timer — расписание.

## Конфигурация

Пример лежит в `deploy/config.example.yaml`.

| Поле | Смысл |
| --- | --- |
| `s3.endpoint` | URL S3-совместимого API. Для AWS можно оставить пустым. |
| `s3.region` | Регион. Для многих совместимых хранилищ подойдёт `us-east-1`. |
| `s3.bucket` | Бакет. |
| `s3.prefix` | Префикс ключей этого VPS, например `vps-1`. |
| `s3.force_path_style` | `true` для хранилищ, которым нужен path-style. |
| `s3.retention_days` | `0` — старые объекты не трогать. Больше нуля — после успешного дампа удалить объекты этой базы старше срока. |
| `dump.tmp_dir` | Каталог временных архивов. По умолчанию `/var/lib/dumper`. |
| `targets[].name` | Имя цели в ключе объекта. |
| `targets[].container` | Имя контейнера Postgres. |
| `targets[].database` | База или `"*"` для всех баз, кроме шаблонов. |
| `targets[].user` | Роль, от которой запускается `pg_dump`. |
| `targets[].password` | Необязательный пароль. Если поле пустое, берётся `POSTGRES_PASSWORD` контейнера. |

Кавычки у `database: "*"` обязательны: в YAML звёздочка без кавычек — это alias.

Пароль выбирается так:

1. Поле `password` цели, если оно задано.
2. Иначе переменная `POSTGRES_PASSWORD` из окружения контейнера.
3. Если пароля нет, `pg_dump` использует локальную аутентификацию внутри контейнера.

Ключи S3 задаются полями `s3.access_key_id` и `s3.secret_access_key` либо переменными `AWS_ACCESS_KEY_ID` и `AWS_SECRET_ACCESS_KEY`.

`database: "*"` подключается к базе `postgres` и читает список через `psql`. В выборку попадают базы с `datallowconn`, шаблоны (`datistemplate`) пропускаются.

На `dump.tmp_dir` нужно свободное место размером с дамп. Custom format уже сжат внутри архива, отдельный gzip не используется. После загрузки временный файл удаляется.

Сервис systemd запускается от root: доступ к `/var/run/docker.sock` равносилен root на хосте. Файл конфига с секретами держите в режиме `0600`. Если права шире, агент пишет предупреждение и всё равно стартует.

Для бакета нужны права на `HeadBucket`, `PutObject` и, при `retention_days` больше нуля, на `ListBucket` и `DeleteObject`. Объекты больше 16 МиБ заливаются частями по 8 МиБ.

Цели обрабатываются по очереди. Ошибка одной не отменяет остальные. Код выхода ненулевой, если хотя бы одна цель не удалась.

## Lifecycle

При `retention_days: 0` срок хранения задаёт правило бакета. Пример на 14 дней для префикса `vps-1/`:

```sh
aws s3api put-bucket-lifecycle-configuration \
  --endpoint-url https://s3.example.com \
  --bucket backups \
  --lifecycle-configuration '{
    "Rules": [{
      "ID": "expire-dumps",
      "Status": "Enabled",
      "Filter": {"Prefix": "vps-1/"},
      "Expiration": {"Days": 14}
    }]
  }'
```

## Восстановление

Архив в custom format читает `pg_restore`, не `psql`.

```sh
aws s3 cp s3://backups/vps-1/myapp/myapp/20261017T010203Z.dump ./myapp.dump \
  --endpoint-url https://s3.example.com
pg_restore --clean --if-exists -d myapp myapp.dump
```

## Команды

```text
dumper backup [-config /etc/dumper/config.yaml] [-only myapp]
dumper check [-config /etc/dumper/config.yaml]
dumper install
```

`backup` снимает дампы и заливает их. `-only` ограничивается одной целью. `check` читает конфиг, проверяет, что контейнеры запущены, и делает `HeadBucket`. `install` копирует бинарник, пример конфига и unit-файлы systemd, затем выполняет `systemctl daemon-reload`. Существующий конфиг не перезаписывается. Таймер не включается.

## Разработка

```sh
make test
```
