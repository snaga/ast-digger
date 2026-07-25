package mixedrefs;

public class Caller {
    public static void callSite() {
        Target.helperFn(); // actual reference on this line
    }
}
