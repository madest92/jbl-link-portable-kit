/* Плагин входа для штатного Fluent Bit 1.4 (ARMv7, glibc колонки).
 *
 * Fluent Bit вызывает init при старте; плагин запускает «/data/jbl-boot/device.sh boot» в отвязанном
 * процессе и сразу возвращает 0: записей не даёт, штатные логи работают как раньше. Между fork и
 * execve — только async-signal-safe вызовы: у Fluent Bit к этому моменту уже могут быть потоки.
 * Заголовков libc нет (сборка без sysroot): объявления — вручную, символы берутся из процесса
 * Fluent Bit при загрузке. */

extern int fork(void);
extern int setsid(void);
extern int open(const char *path, int flags, ...);
extern int close(int fd);
extern int dup2(int from, int to);
extern int execve(const char *path, char *const argv[], char *const envp[]);
extern int waitpid(int pid, int *status, int options);
extern void _exit(int code);
extern unsigned int geteuid(void);

#define DEVICE_SH "/data/jbl-boot/device.sh"
#define LOG "/run/jbl-boot.log"

/* флаги open для ARM Linux */
enum { RD = 0, WR_APPEND_CREATE = 01 | 0100 | 02000 };

static char *const boot_argv[] = {"sh", DEVICE_SH, "boot", 0};
static char *const boot_envp[] = {"PATH=/usr/sbin:/usr/bin:/sbin:/bin", 0};

/* потомок: без дескрипторов Fluent Bit, stdin — /dev/null, вывод — в журнал, затем device.sh boot */
static void run_boot(void)
{
    int fd;

    if (fork() != 0) /* промежуточный процесс выходит: device.sh усыновит init, зомби не будет */
        _exit(0);
    setsid();
    for (fd = 0; fd < 256; fd++)
        close(fd);
    open("/dev/null", RD);           /* 0 */
    if (open(LOG, WR_APPEND_CREATE, 0600) == 1) /* 1 */
        dup2(1, 2);
    execve("/bin/sh", boot_argv, boot_envp);
    _exit(127);
}

static int on_init(void *instance, void *config, void *data)
{
    int pid, status;

    (void)instance;
    (void)config;
    (void)data;
    if (geteuid() != 0)
        return 0;
    pid = fork();
    if (pid == 0)
        run_boot();
    if (pid > 0)
        waitpid(pid, &status, 0);
    return 0; /* сбой запуска не должен останавливать Fluent Bit */
}

/* struct flb_input_plugin Fluent Bit 1.4.6 на 32 битах — 16 слов: флаги, имя, описание,
 * config_map, девять обработчиков (первый — init), instance, узел списка. */
struct fb_input_plugin {
    int flags;
    const char *name;
    const char *description;
    void *config_map;
    int (*init)(void *, void *, void *);
    void *other_callbacks[8];
    void *instance;
    void *list[2];
};
_Static_assert(sizeof(struct fb_input_plugin) == 16 * 4, "раскладка не ARM32");

/* имя символа Fluent Bit выводит из имени файла: flb-in_jbl_boot.so → in_jbl_boot_plugin */
__attribute__((visibility("default"))) struct fb_input_plugin in_jbl_boot_plugin = {
    .name = "jbl_boot",
    .description = "jbl-link-portable-kit: hooks at boot",
    .init = on_init,
};
